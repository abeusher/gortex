package contracts

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func rustRouteContracts(cs []Contract) map[string]Contract {
	out := map[string]Contract{}
	for _, c := range cs {
		if c.Type == ContractHTTP && c.Role == RoleProvider {
			out[c.ID] = c
		}
	}
	return out
}

func TestRustAxum_ChainedMethods(t *testing.T) {
	src := []byte(`use axum::{Router, routing::{get, post}};

fn app() -> Router {
    Router::new().route("/u", get(list).post(create))
}

async fn list() {}
async fn create() {}
`)
	nodes := []*graph.Node{
		{ID: "api.rs::list", Name: "list", Kind: graph.KindFunction, FilePath: "api.rs", StartLine: 7, EndLine: 7},
		{ID: "api.rs::create", Name: "create", Kind: graph.KindFunction, FilePath: "api.rs", StartLine: 8, EndLine: 8},
	}
	by := rustRouteContracts((&HTTPExtractor{}).Extract("api.rs", src, nodes, nil))

	get, ok := by["http::GET::/u"]
	if !ok {
		t.Fatalf("no GET /u contract (chained route not split)")
	}
	if get.SymbolID != "api.rs::list" {
		t.Errorf("GET /u SymbolID = %q (want api.rs::list)", get.SymbolID)
	}
	post, ok := by["http::POST::/u"]
	if !ok {
		t.Fatalf("no POST /u contract (chained second method missed)")
	}
	if post.SymbolID != "api.rs::create" {
		t.Errorf("POST /u SymbolID = %q (want api.rs::create)", post.SymbolID)
	}
}

func TestRustActix_BuilderResource(t *testing.T) {
	src := []byte(`use actix_web::web;

fn config(cfg: &mut web::ServiceConfig) {
    cfg.service(web::resource("/u").route(web::get().to(list)));
}

async fn list() {}
`)
	nodes := []*graph.Node{
		{ID: "api.rs::list", Name: "list", Kind: graph.KindFunction, FilePath: "api.rs", StartLine: 7, EndLine: 7},
	}
	by := rustRouteContracts((&HTTPExtractor{}).Extract("api.rs", src, nodes, nil))

	c, ok := by["http::GET::/u"]
	if !ok {
		t.Fatalf("no GET /u contract from the Actix builder form")
	}
	if c.SymbolID != "api.rs::list" {
		t.Errorf("SymbolID = %q (want api.rs::list)", c.SymbolID)
	}
	if fw, _ := c.Meta["framework"].(string); fw != "actix" {
		t.Errorf("framework = %q (want actix)", fw)
	}
}

func TestRustActix_ScopePrefix(t *testing.T) {
	src := []byte(`use actix_web::{web, App};

fn app() {
    App::new().service(
        web::scope("/api")
            .service(web::resource("/users").route(web::get().to(list)))
    );
}

async fn list() {}
`)
	nodes := []*graph.Node{
		{ID: "api.rs::list", Name: "list", Kind: graph.KindFunction, FilePath: "api.rs", StartLine: 10, EndLine: 10},
	}
	by := rustRouteContracts((&HTTPExtractor{}).Extract("api.rs", src, nodes, nil))

	if _, ok := by["http::GET::/api/users"]; !ok {
		ids := make([]string, 0, len(by))
		for id := range by {
			ids = append(ids, id)
		}
		t.Fatalf("no GET /api/users contract (web::scope prefix not joined); got %v", ids)
	}
}

func TestRustActix_TwoResourcesOnlyScopedOnePrefixed(t *testing.T) {
	// A resource outside the scope must not inherit the scope prefix.
	src := []byte(`use actix_web::{web, App};

fn app() {
    App::new()
        .service(web::scope("/api").service(web::resource("/in").route(web::get().to(a))))
        .service(web::resource("/out").route(web::get().to(b)));
}

async fn a() {}
async fn b() {}
`)
	nodes := []*graph.Node{
		{ID: "api.rs::a", Name: "a", Kind: graph.KindFunction, FilePath: "api.rs", StartLine: 9, EndLine: 9},
		{ID: "api.rs::b", Name: "b", Kind: graph.KindFunction, FilePath: "api.rs", StartLine: 10, EndLine: 10},
	}
	by := rustRouteContracts((&HTTPExtractor{}).Extract("api.rs", src, nodes, nil))

	if _, ok := by["http::GET::/api/in"]; !ok {
		t.Errorf("scoped resource should be /api/in")
	}
	if _, ok := by["http::GET::/out"]; !ok {
		t.Errorf("unscoped resource should stay /out, not inherit /api")
	}
}
