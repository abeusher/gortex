package contracts

import "strings"

// CrossLink represents a matched provider-consumer pair, possibly across repos.
type CrossLink struct {
	ContractID string   `json:"contract_id"`
	Provider   Contract `json:"provider"`
	Consumer   Contract `json:"consumer"`
	CrossRepo  bool     `json:"cross_repo"`
}

// MatchResult holds the output of a matching pass.
type MatchResult struct {
	Matched         []CrossLink `json:"matched"`
	OrphanProviders []Contract  `json:"orphan_providers"`
	OrphanConsumers []Contract  `json:"orphan_consumers"`
}

// Match analyses a registry and pairs providers with consumers by
// contract ID, bounded by the (workspace, project) boundary:
//
//   - Providers and consumers in different effective workspaces never
//     pair. Each workspace is matched independently — the across-
//     workspace contracts become orphans on their own side.
//   - Providers and consumers in the same workspace but different
//     projects do not pair either: a project owns its own surface and
//     a sibling project's consumer is treated as an orphan that needs
//     an explicit inter-project import to wire up. Iteration 1 keeps
//     it simple: orphan rather than pair.
//
// "Effective" means: WorkspaceID / ProjectID if set, else RepoPrefix —
// the "missing → repo-name" default. So the previous behaviour (one
// repo = one workspace = one project) still drops out for callers
// that haven't started populating the slugs yet.
//
// The CrossRepo flag stays on a CrossLink whose provider and consumer
// have different RepoPrefixes (legitimately so — two repos belonging
// to one workspace, e.g. `tuck-api` provider matched with `tuck-app`
// consumer when both declare WorkspaceID = "tuck").
//
// After the exact-ID pairing, a second pass joins the RPC family
// (gRPC + Thrift, plus tRPC in its own cohort) by canonical
// service/method names — see joinRPCCanonical. IDL definitions and
// generated-stub call sites frequently disagree on the literal
// contract ID (package-qualified vs bare service names, camelCase vs
// PascalCase method casing, service-level registrations vs
// method-level calls); for tRPC the router-namespace spelling differs
// between the server's router variable and the client's proxy chain
// while the procedure name stays stable. The canonical join recovers
// those pairs so cross-service traversal doesn't stop at a spelling
// difference.
func Match(reg *Registry) MatchResult {
	var result MatchResult

	// Collect every contract once (the byID lists already cover all
	// contracts) and bucket them by (effectiveWorkspace,
	// effectiveProject, ID, role). We can't just iterate AllIDs and
	// then split by workspace/project because two providers for the
	// same ID in different projects must be reported as separate
	// orphan groups, not lumped together.
	type bucketKey struct {
		workspace string
		project   string
		id        string
	}
	providers := make(map[bucketKey][]Contract)
	consumers := make(map[bucketKey][]Contract)

	for _, id := range reg.AllIDs() {
		for _, c := range reg.ByID(id) {
			key := bucketKey{
				workspace: c.EffectiveWorkspace(),
				project:   c.EffectiveProject(),
				id:        id,
			}
			switch c.Role {
			case RoleProvider:
				providers[key] = append(providers[key], c)
			case RoleConsumer:
				consumers[key] = append(consumers[key], c)
			}
		}
	}

	// Pair within each bucket; emit matched links plus orphans.
	seen := make(map[bucketKey]struct{})
	for key, provs := range providers {
		seen[key] = struct{}{}
		cons := consumers[key]
		if len(cons) == 0 {
			result.OrphanProviders = append(result.OrphanProviders, provs...)
			continue
		}
		for _, consumer := range cons {
			for _, provider := range provs {
				result.Matched = append(result.Matched, CrossLink{
					ContractID: key.id,
					Provider:   provider,
					Consumer:   consumer,
					CrossRepo:  provider.RepoPrefix != consumer.RepoPrefix,
				})
			}
		}
	}
	for key, cons := range consumers {
		if _, ok := seen[key]; ok {
			continue
		}
		// No provider in this bucket — every consumer is orphaned.
		// Orphan, never pair across the boundary even when an
		// ID-equivalent exists in a sibling workspace.
		result.OrphanConsumers = append(result.OrphanConsumers, cons...)
	}

	joinRPCCanonical(&result)
	joinTRPCCanonical(&result)

	return result
}

// isRPCFamily reports whether a contract belongs to the RPC IDL
// family the canonical-name join pairs across. gRPC and Thrift share
// the same generated-stub surface (`New<Service>Client(...)`), so a
// code-side consumer detected as grpc legitimately pairs with a
// thrift IDL definition of the same service.
func isRPCFamily(c Contract) bool {
	return c.Type == ContractGRPC || c.Type == ContractThrift
}

// rpcServiceMethod extracts the canonical (service, method) join key
// from an RPC-family contract, both lowercased. The service name is
// stripped of any namespace/package qualifier (`billing.v1.Users` →
// `users`) and methods compare case-insensitively because generated
// stubs re-case them per language convention (Go GetUser vs TS
// getUser). Falls back to parsing the contract ID's
// `<type>::<Service>[::<Method>]` segments when Meta is missing. An
// empty method means the contract is service-level (a client
// construction or a server registration without method granularity).
func rpcServiceMethod(c Contract) (service, method string) {
	if c.Meta != nil {
		service, _ = c.Meta["service"].(string)
		method, _ = c.Meta["method"].(string)
	}
	if service == "" || method == "" {
		parts := strings.Split(c.ID, "::")
		if service == "" && len(parts) >= 2 {
			service = parts[1]
		}
		if method == "" && len(parts) >= 3 {
			method = parts[2]
		}
	}
	if dot := strings.LastIndex(service, "."); dot >= 0 {
		service = service[dot+1:]
	}
	return strings.ToLower(service), strings.ToLower(method)
}

// rpcGroupID picks the contract ID that names the joined group: the
// method-level side wins (it is strictly more specific), provider
// first so two method-level sides group under the provider's ID — the
// ID every exact-matched link for the same RPC already uses.
func rpcGroupID(provider, consumer Contract) string {
	if _, pm := rpcServiceMethod(provider); pm != "" {
		return provider.ID
	}
	if _, cm := rpcServiceMethod(consumer); cm != "" {
		return consumer.ID
	}
	return provider.ID
}

// matcherIdentity is the per-record identity key the orphan-removal
// bookkeeping uses. Mirrors removeContract's field set so two registry
// entries that the Registry treats as distinct stay distinct here.
func matcherIdentity(c Contract) string {
	return c.ID + "|" + c.FilePath + "|" + c.SymbolID + "|" + string(c.Role) + "|" + c.RepoPrefix
}

// joinRPCCanonical pairs the RPC-family orphans left over from exact-
// ID matching by canonical service/method names, within the same
// (workspace, project) boundary the exact pass uses. Three shapes are
// recovered:
//
//   - method-level consumer ↔ method-level provider whose IDs differ
//     only in service qualification or method casing (TS camelCase
//     stubs vs proto PascalCase RPCs);
//   - service-level consumer (bare client construction) ↔ every
//     provider of that service;
//   - service-level provider (Go `Register<Service>Server` site) ↔
//     every consumer of that service.
//
// Joined contracts are removed from the orphan lists; the emitted
// CrossLinks group under the method-level side's contract ID (see
// rpcGroupID) so bridge materialisation keeps per-RPC granularity.
func joinRPCCanonical(result *MatchResult) {
	type svcKey struct{ ws, proj, svc string }
	type methodKey struct {
		ws, proj, svc, method string
	}

	// Index every RPC-family contract on BOTH sides of the existing
	// result — matched and orphaned. A service-level orphan must be
	// able to join contracts that already exact-matched (e.g. a TS
	// client construction joining a proto RPC that a Go consumer
	// already paired with).
	var allProviders, allConsumers []Contract
	for _, m := range result.Matched {
		allProviders = append(allProviders, m.Provider)
		allConsumers = append(allConsumers, m.Consumer)
	}
	allProviders = append(allProviders, result.OrphanProviders...)
	allConsumers = append(allConsumers, result.OrphanConsumers...)

	provByMethod := make(map[methodKey][]Contract)
	provBySvc := make(map[svcKey][]Contract)
	provSeen := make(map[string]struct{})
	for _, p := range allProviders {
		if !isRPCFamily(p) {
			continue
		}
		// The matched list repeats a provider once per consumer it
		// paired with; index each record once.
		idKey := matcherIdentity(p)
		if _, dup := provSeen[idKey]; dup {
			continue
		}
		provSeen[idKey] = struct{}{}
		svc, method := rpcServiceMethod(p)
		if svc == "" {
			continue
		}
		sk := svcKey{p.EffectiveWorkspace(), p.EffectiveProject(), svc}
		provBySvc[sk] = append(provBySvc[sk], p)
		if method != "" {
			provByMethod[methodKey{sk.ws, sk.proj, svc, method}] = append(
				provByMethod[methodKey{sk.ws, sk.proj, svc, method}], p)
		}
	}

	consByMethod := make(map[methodKey][]Contract)
	consBySvc := make(map[svcKey][]Contract)
	consSeen := make(map[string]struct{})
	for _, c := range allConsumers {
		if !isRPCFamily(c) {
			continue
		}
		idKey := matcherIdentity(c)
		if _, dup := consSeen[idKey]; dup {
			continue
		}
		consSeen[idKey] = struct{}{}
		svc, method := rpcServiceMethod(c)
		if svc == "" {
			continue
		}
		sk := svcKey{c.EffectiveWorkspace(), c.EffectiveProject(), svc}
		consBySvc[sk] = append(consBySvc[sk], c)
		if method != "" {
			consByMethod[methodKey{sk.ws, sk.proj, svc, method}] = append(
				consByMethod[methodKey{sk.ws, sk.proj, svc, method}], c)
		}
	}

	joinedProv := make(map[string]struct{})
	joinedCons := make(map[string]struct{})
	linked := make(map[string]struct{})
	emit := func(p, c Contract) {
		lk := matcherIdentity(p) + "->" + matcherIdentity(c)
		if _, dup := linked[lk]; dup {
			return
		}
		linked[lk] = struct{}{}
		result.Matched = append(result.Matched, CrossLink{
			ContractID: rpcGroupID(p, c),
			Provider:   p,
			Consumer:   c,
			CrossRepo:  p.RepoPrefix != c.RepoPrefix,
		})
		joinedProv[matcherIdentity(p)] = struct{}{}
		joinedCons[matcherIdentity(c)] = struct{}{}
	}

	// Orphan consumers seek providers.
	for _, c := range result.OrphanConsumers {
		if !isRPCFamily(c) {
			continue
		}
		svc, method := rpcServiceMethod(c)
		if svc == "" {
			continue
		}
		sk := svcKey{c.EffectiveWorkspace(), c.EffectiveProject(), svc}
		if method != "" {
			provs := provByMethod[methodKey{sk.ws, sk.proj, svc, method}]
			if len(provs) == 0 {
				// No method-level provider — fall back to service-
				// level providers only. Joining a different method's
				// provider would be wrong.
				for _, p := range provBySvc[sk] {
					if _, pm := rpcServiceMethod(p); pm == "" {
						provs = append(provs, p)
					}
				}
			}
			for _, p := range provs {
				emit(p, c)
			}
			continue
		}
		// Service-level consumer joins every provider of the service.
		for _, p := range provBySvc[sk] {
			emit(p, c)
		}
	}

	// Orphan providers seek consumers (covers the registration-site
	// provider whose consumers all exact-matched the IDL definition).
	for _, p := range result.OrphanProviders {
		if !isRPCFamily(p) {
			continue
		}
		if _, done := joinedProv[matcherIdentity(p)]; done {
			continue
		}
		svc, method := rpcServiceMethod(p)
		if svc == "" {
			continue
		}
		sk := svcKey{p.EffectiveWorkspace(), p.EffectiveProject(), svc}
		if method != "" {
			cons := consByMethod[methodKey{sk.ws, sk.proj, svc, method}]
			if len(cons) == 0 {
				for _, c := range consBySvc[sk] {
					if _, cm := rpcServiceMethod(c); cm == "" {
						cons = append(cons, c)
					}
				}
			}
			for _, c := range cons {
				emit(p, c)
			}
			continue
		}
		for _, c := range consBySvc[sk] {
			emit(p, c)
		}
	}

	if len(joinedProv) > 0 {
		kept := result.OrphanProviders[:0]
		for _, p := range result.OrphanProviders {
			if _, done := joinedProv[matcherIdentity(p)]; done {
				continue
			}
			kept = append(kept, p)
		}
		result.OrphanProviders = kept
	}
	if len(joinedCons) > 0 {
		kept := result.OrphanConsumers[:0]
		for _, c := range result.OrphanConsumers {
			if _, done := joinedCons[matcherIdentity(c)]; done {
				continue
			}
			kept = append(kept, c)
		}
		result.OrphanConsumers = kept
	}
}

// trpcRouterProcedure extracts the canonical join components of a tRPC
// contract: the router namespace and the procedure name, both
// lowercased. The procedure is the stable join key — client and server
// always agree on it — while the router is the part that drifts: the
// server names it after the variable the router object is assigned to
// (`export const userRouter = createTRPCRouter({...})`) and the client
// reaches it through a proxy chain whose first segment is spelled to
// taste (`trpc.api.getUser`). The router is returned only so the join
// can detect genuine ambiguity; it is deliberately NOT part of the key.
// Reads Meta["router"]/Meta["procedure"], falling back to parsing the
// `trpc::<router>.<procedure>` contract ID.
func trpcRouterProcedure(c Contract) (router, procedure string) {
	if c.Meta != nil {
		router, _ = c.Meta["router"].(string)
		procedure, _ = c.Meta["procedure"].(string)
	}
	if router == "" || procedure == "" {
		// ID shape: trpc::<router>.<procedure>. The router is a single
		// identifier and the procedure an object key, so exactly one dot
		// separates them; LastIndex keeps the procedure intact even if a
		// future router form carried its own dots.
		rest := strings.TrimPrefix(c.ID, "trpc::")
		if dot := strings.LastIndex(rest, "."); dot >= 0 {
			if router == "" {
				router = rest[:dot]
			}
			if procedure == "" {
				procedure = rest[dot+1:]
			}
		}
	}
	return strings.ToLower(router), strings.ToLower(procedure)
}

// joinTRPCCanonical pairs the tRPC orphans left over from exact-ID
// matching, within the same (workspace, project) boundary the exact
// pass uses. tRPC is its OWN cohort: this pass only ever touches
// ContractTRPC records, and the RPC-family pass (joinRPCCanonical /
// isRPCFamily) deliberately excludes tRPC, so a tRPC consumer can never
// pair a gRPC or Thrift provider — the two never share an index.
//
// The canonical key is the lowercased PROCEDURE name alone. The
// server's router variable and the client's proxy-chain namespace
// routinely disagree on spelling while the procedure name stays stable,
// so keying on the procedure (and dropping the router from the key)
// recovers a client call to its server procedure regardless of how the
// router was spelled or cased — mirroring how joinRPCCanonical strips a
// service's package qualifier and compares methods case-insensitively.
//
// Precision over recall: a procedure name that more than one distinct
// provider router offers within the boundary is genuinely ambiguous —
// the consumer's plain `trpc.<ns>.<proc>` chain can't say which router
// it meant — so such procedures are NOT joined and stay orphans. A
// procedure offered by exactly one provider router joins regardless of
// the router-namespace spelling the consumer used. Joined links group
// under the provider's contract ID (the server's spelling is
// authoritative) so bridge materialisation collapses every spelling of
// one server procedure into a single group.
func joinTRPCCanonical(result *MatchResult) {
	type procKey struct{ ws, proj, proc string }

	// Index every tRPC contract on BOTH sides of the result — matched
	// and orphaned — so an orphan can join a side that already
	// exact-matched a differently-spelled counterpart.
	var allProviders, allConsumers []Contract
	for _, m := range result.Matched {
		allProviders = append(allProviders, m.Provider)
		allConsumers = append(allConsumers, m.Consumer)
	}
	allProviders = append(allProviders, result.OrphanProviders...)
	allConsumers = append(allConsumers, result.OrphanConsumers...)

	provByProc := make(map[procKey][]Contract)
	provRouters := make(map[procKey]map[string]struct{})
	provSeen := make(map[string]struct{})
	for _, p := range allProviders {
		if p.Type != ContractTRPC {
			continue
		}
		idKey := matcherIdentity(p)
		if _, dup := provSeen[idKey]; dup {
			continue
		}
		provSeen[idKey] = struct{}{}
		router, proc := trpcRouterProcedure(p)
		if proc == "" {
			continue
		}
		pk := procKey{p.EffectiveWorkspace(), p.EffectiveProject(), proc}
		provByProc[pk] = append(provByProc[pk], p)
		if provRouters[pk] == nil {
			provRouters[pk] = make(map[string]struct{})
		}
		provRouters[pk][router] = struct{}{}
	}

	consByProc := make(map[procKey][]Contract)
	consSeen := make(map[string]struct{})
	for _, c := range allConsumers {
		if c.Type != ContractTRPC {
			continue
		}
		idKey := matcherIdentity(c)
		if _, dup := consSeen[idKey]; dup {
			continue
		}
		consSeen[idKey] = struct{}{}
		_, proc := trpcRouterProcedure(c)
		if proc == "" {
			continue
		}
		pk := procKey{c.EffectiveWorkspace(), c.EffectiveProject(), proc}
		consByProc[pk] = append(consByProc[pk], c)
	}

	// A procedure offered by more than one distinct provider router is
	// ambiguous on the procedure name alone — skip the whole procedure
	// (both join directions) so an ambiguous name never mints a guess.
	ambiguous := func(pk procKey) bool { return len(provRouters[pk]) > 1 }

	joinedProv := make(map[string]struct{})
	joinedCons := make(map[string]struct{})
	linked := make(map[string]struct{})
	emit := func(p, c Contract) {
		lk := matcherIdentity(p) + "->" + matcherIdentity(c)
		if _, dup := linked[lk]; dup {
			return
		}
		linked[lk] = struct{}{}
		result.Matched = append(result.Matched, CrossLink{
			ContractID: p.ID,
			Provider:   p,
			Consumer:   c,
			CrossRepo:  p.RepoPrefix != c.RepoPrefix,
		})
		joinedProv[matcherIdentity(p)] = struct{}{}
		joinedCons[matcherIdentity(c)] = struct{}{}
	}

	// Orphan consumers seek a provider of the same procedure.
	for _, c := range result.OrphanConsumers {
		if c.Type != ContractTRPC {
			continue
		}
		_, proc := trpcRouterProcedure(c)
		if proc == "" {
			continue
		}
		pk := procKey{c.EffectiveWorkspace(), c.EffectiveProject(), proc}
		if ambiguous(pk) {
			continue
		}
		for _, p := range provByProc[pk] {
			emit(p, c)
		}
	}

	// Orphan providers seek consumers of the same procedure (covers a
	// provider whose same-spelled consumers all exact-matched, leaving a
	// differently-spelled consumer orphaned in another repo).
	for _, p := range result.OrphanProviders {
		if p.Type != ContractTRPC {
			continue
		}
		if _, done := joinedProv[matcherIdentity(p)]; done {
			continue
		}
		_, proc := trpcRouterProcedure(p)
		if proc == "" {
			continue
		}
		pk := procKey{p.EffectiveWorkspace(), p.EffectiveProject(), proc}
		if ambiguous(pk) {
			continue
		}
		for _, c := range consByProc[pk] {
			emit(p, c)
		}
	}

	if len(joinedProv) > 0 {
		kept := result.OrphanProviders[:0]
		for _, p := range result.OrphanProviders {
			if _, done := joinedProv[matcherIdentity(p)]; done {
				continue
			}
			kept = append(kept, p)
		}
		result.OrphanProviders = kept
	}
	if len(joinedCons) > 0 {
		kept := result.OrphanConsumers[:0]
		for _, c := range result.OrphanConsumers {
			if _, done := joinedCons[matcherIdentity(c)]; done {
				continue
			}
			kept = append(kept, c)
		}
		result.OrphanConsumers = kept
	}
}
