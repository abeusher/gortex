package contracts

import (
	"testing"
)

// mpset collects provider "METHOD path" keys from a contract slice.
func mpset(cs []Contract) map[string]bool {
	out := map[string]bool{}
	for _, c := range cs {
		if c.Role != RoleProvider {
			continue
		}
		m, _ := c.Meta["method"].(string)
		p, _ := c.Meta["path"].(string)
		out[m+" "+p] = true
	}
	return out
}

func assertRoutes(t *testing.T, filePath, src string, want ...string) map[string]bool {
	t.Helper()
	h := &HTTPExtractor{}
	got := mpset(h.Extract(filePath, []byte(src), nil, nil))
	for _, w := range want {
		if !got[w] {
			t.Errorf("%s: missing route %q (got: %v)", filePath, w, keysOfBool(got))
		}
	}
	return got
}

// TestNextRouteExtraction proves Next.js file routing: app-router directory
// routes (page.tsx -> the dir, dynamic [id] -> {p1}, root -> /), app-router API
// route.ts handlers expanded per exported method, and pages-router filename
// routes with index collapsing to the parent. The route lands on the canonical
// http::METHOD::path model — so it pairs with fetch consumers — which a
// page-file scanner that only reads content cannot derive.
func TestNextRouteExtraction(t *testing.T) {
	assertRoutes(t, "app/users/[id]/page.tsx",
		"export default function Page(){ return null }",
		"GET /users/{p1}")

	assertRoutes(t, "app/page.tsx",
		"export default function Home(){ return null }",
		"GET /")

	// Route groups contribute no URL segment.
	assertRoutes(t, "app/(marketing)/about/page.tsx",
		"export default function About(){ return null }",
		"GET /about")

	got := assertRoutes(t, "app/api/posts/route.ts",
		"export async function GET(){}\nexport async function POST(){}",
		"GET /api/posts", "POST /api/posts")
	if len(got) != 2 {
		t.Errorf("app/api route.ts: expected exactly 2 method routes, got %v", keysOfBool(got))
	}

	assertRoutes(t, "pages/users/[id].tsx",
		"export default function U(){ return null }",
		"GET /users/{p1}")
	assertRoutes(t, "pages/index.tsx",
		"export default function H(){ return null }",
		"GET /")
}

// TestNuxtRouteExtraction proves Nuxt routing: server/api modules (with a
// `.get.ts` method suffix and the /api prefix) and pages/*.vue page routes.
func TestNuxtRouteExtraction(t *testing.T) {
	assertRoutes(t, "server/api/users/[id].get.ts",
		"export default defineEventHandler(() => ({}))",
		"GET /api/users/{p1}")

	assertRoutes(t, "server/api/posts.post.ts",
		"export default defineEventHandler(() => ({}))",
		"POST /api/posts")

	assertRoutes(t, "pages/about.vue",
		"<template><div/></template>",
		"GET /about")

	// Catch-all [...slug] -> a single path parameter.
	assertRoutes(t, "pages/docs/[...slug].vue",
		"<template><div/></template>",
		"GET /docs/{p1}")
}

// TestSvelteKitRouteExtraction proves SvelteKit routing: +server.ts API routes
// (one per exported method) and +page route pages, both under src/routes.
func TestSvelteKitRouteExtraction(t *testing.T) {
	got := assertRoutes(t, "src/routes/users/[id]/+server.ts",
		"export function GET(){}\nexport function DELETE(){}",
		"GET /users/{p1}", "DELETE /users/{p1}")
	if len(got) != 2 {
		t.Errorf("+server.ts: expected 2 method routes, got %v", keysOfBool(got))
	}

	assertRoutes(t, "src/routes/blog/[slug]/+page.svelte",
		"<h1>Post</h1>",
		"GET /blog/{p1}")

	// A +layout is not itself a route.
	h := &HTTPExtractor{}
	if got := mpset(h.Extract("src/routes/+layout.svelte", []byte("<slot/>"), nil, nil)); len(got) != 0 {
		t.Errorf("+layout.svelte should produce no routes, got %v", keysOfBool(got))
	}
}

// TestAstroRouteExtraction proves Astro routing: .astro pages and .ts/.js
// endpoint modules under src/pages.
func TestAstroRouteExtraction(t *testing.T) {
	assertRoutes(t, "src/pages/posts/[id].astro",
		"---\nconst { id } = Astro.params;\n---\n<h1>{id}</h1>",
		"GET /posts/{p1}")

	assertRoutes(t, "src/pages/index.astro",
		"---\n---\n<h1>Home</h1>",
		"GET /")

	got := assertRoutes(t, "src/pages/api/data.ts",
		"export async function GET(){}\nexport async function POST(){}",
		"GET /api/data", "POST /api/data")
	if len(got) != 2 {
		t.Errorf("astro endpoint: expected 2 method routes, got %v", keysOfBool(got))
	}
}

// TestReactRouterExtraction proves React Router routing from both the JSX
// `<Route path=...>` form and the createBrowserRouter object-config form, on
// the canonical path model.
func TestReactRouterExtraction(t *testing.T) {
	assertRoutes(t, "src/App.tsx",
		"const router = createBrowserRouter([\n  { path: '/dashboard', element: <D/> },\n  { path: '/users/:id', element: <U/> },\n]);",
		"GET /dashboard", "GET /users/{p1}")

	assertRoutes(t, "src/Routes.jsx",
		"<Routes>\n  <Route path='/home' element={<Home/>} />\n  <Route path='/settings' element={<Settings/>} />\n</Routes>",
		"GET /home", "GET /settings")
}
