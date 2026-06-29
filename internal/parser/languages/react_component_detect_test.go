package languages

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func uiComp(nodes []*graph.Node, name string) string {
	n := nodeByName(nodes, name)
	if n == nil || n.Meta == nil {
		return ""
	}
	v, _ := n.Meta["ui_component"].(string)
	return v
}

func componentKind(nodes []*graph.Node, name string) string {
	n := nodeByName(nodes, name)
	if n == nil || n.Meta == nil {
		return ""
	}
	v, _ := n.Meta["component_kind"].(string)
	return v
}

// TestReactDetect_FunctionAndArrowComponents covers the JSX-rendering
// PascalCase positives: a function declaration, an arrow const, a
// fragment-returning arrow, and a primitive-only render.
func TestReactDetect_FunctionAndArrowComponents(t *testing.T) {
	src := []byte(`import React from 'react'

function Card() { return <div><Inner/></div> }
const Panel = () => <section/>
const Frag = () => <>hi</>
function Prim() { return <div/> }
`)
	res, err := NewTypeScriptExtractor().Extract("App.tsx", src)
	require.NoError(t, err)

	assert.Equal(t, "react", uiComp(res.Nodes, "Card"))
	assert.Equal(t, "function", componentKind(res.Nodes, "Card"))
	assert.Equal(t, "react", uiComp(res.Nodes, "Panel"))
	assert.Equal(t, "arrow", componentKind(res.Nodes, "Panel"))
	assert.Equal(t, "react", uiComp(res.Nodes, "Frag"))
	assert.Equal(t, "react", uiComp(res.Nodes, "Prim"), "a primitive-only render is still a component")
}

// TestReactDetect_FrameworkGating proves the framework value is
// import-gated: solid-js / preact resolve to their own names, and a
// JSX file with no framework import falls back to the neutral "jsx".
func TestReactDetect_FrameworkGating(t *testing.T) {
	solid, err := NewTypeScriptExtractor().Extract("S.tsx",
		[]byte("import { render } from 'solid-js'\nfunction App() { return <div/> }\n"))
	require.NoError(t, err)
	assert.Equal(t, "solid", uiComp(solid.Nodes, "App"))

	preact, err := NewTypeScriptExtractor().Extract("P.tsx",
		[]byte("import { h } from 'preact'\nfunction App() { return <div/> }\n"))
	require.NoError(t, err)
	assert.Equal(t, "preact", uiComp(preact.Nodes, "App"))

	neutral, err := NewTypeScriptExtractor().Extract("N.tsx",
		[]byte("function App() { return <div/> }\n"))
	require.NoError(t, err)
	assert.Equal(t, "jsx", uiComp(neutral.Nodes, "App"), "JSX with no framework import is neutral jsx")
}

// TestReactDetect_ClassHeritage covers class components via the extends
// clause (no JSX walk) and the Web-Components base classes.
func TestReactDetect_ClassHeritage(t *testing.T) {
	src := []byte(`import React from 'react'

class Widget extends React.Component { render() { return <div/> } }
class Pure extends PureComponent { render() { return <span/> } }
class Elem extends HTMLElement {}
class Lit extends LitElement {}
class Plain {}
`)
	res, err := NewTypeScriptExtractor().Extract("App.tsx", src)
	require.NoError(t, err)

	assert.Equal(t, "react", uiComp(res.Nodes, "Widget"))
	assert.Equal(t, "class", componentKind(res.Nodes, "Widget"))
	assert.Equal(t, "react", uiComp(res.Nodes, "Pure"))
	assert.Equal(t, "webcomponent", uiComp(res.Nodes, "Elem"))
	assert.Equal(t, "custom_element", componentKind(res.Nodes, "Elem"))
	assert.Equal(t, "webcomponent", uiComp(res.Nodes, "Lit"))
	assert.Equal(t, "", uiComp(res.Nodes, "Plain"), "a plain class is not a component")
}

// TestReactDetect_HOCFramework proves the HOC consts pick up the
// import-resolved framework alongside their component_kind.
func TestReactDetect_HOCFramework(t *testing.T) {
	src := []byte("import { memo, forwardRef } from 'react'\n" +
		"const Button = memo(() => <Spinner/>)\n" +
		"const Card = forwardRef((p, ref) => <Inner/>)\n")
	res, err := NewTypeScriptExtractor().Extract("App.tsx", src)
	require.NoError(t, err)
	assert.Equal(t, "react", uiComp(res.Nodes, "Button"))
	assert.Equal(t, "memo", componentKind(res.Nodes, "Button"))
	assert.Equal(t, "react", uiComp(res.Nodes, "Card"))
}

// TestReactDetect_Precision pins the must-NOT-mark classes: hooks,
// handlers, helpers, and the ancestor false positive.
func TestReactDetect_Precision(t *testing.T) {
	src := []byte(`import React from 'react'

function useThing() { return <div/> }
const handleClick = () => <div/>
const renderRow = () => <tr/>
function App() { const W = () => <div/>; return null }
`)
	res, err := NewTypeScriptExtractor().Extract("App.tsx", src)
	require.NoError(t, err)

	assert.Equal(t, "", uiComp(res.Nodes, "useThing"), "a hook returning JSX is not a component")
	assert.Equal(t, "", uiComp(res.Nodes, "handleClick"), "a handler arrow is not a component")
	assert.Equal(t, "", uiComp(res.Nodes, "renderRow"), "a render helper is not a component")
	assert.Equal(t, "", uiComp(res.Nodes, "App"),
		"App only declares a nested component W and returns null — it must not be marked")
}

// TestReactDetect_TopLevelArrowMarked confirms the W-style nested
// component is itself a component when emitted at top level.
func TestReactDetect_TopLevelArrowMarked(t *testing.T) {
	res, err := NewTypeScriptExtractor().Extract("W.tsx",
		[]byte("import React from 'react'\nconst W = () => <div/>\n"))
	require.NoError(t, err)
	assert.Equal(t, "react", uiComp(res.Nodes, "W"))
}

// TestReactDetect_ClassRenderMethodNotMarked proves only the class node
// carries ui_component — the render() method does not.
func TestReactDetect_ClassRenderMethodNotMarked(t *testing.T) {
	src := []byte(`import React from 'react'

class Widget extends React.Component { render() { return <div/> } }
`)
	res, err := NewTypeScriptExtractor().Extract("App.tsx", src)
	require.NoError(t, err)
	assert.Equal(t, "react", uiComp(res.Nodes, "Widget"))
	for _, n := range res.Nodes {
		if n.Kind == graph.KindMethod {
			_, has := n.Meta["ui_component"]
			assert.False(t, has, "a method (render) must not be marked a component")
		}
	}
}

// TestReactDetect_NoJSXNoComponents proves a plain TS/JS file with no
// JSX produces zero ui_component nodes.
func TestReactDetect_NoJSXNoComponents(t *testing.T) {
	res, err := NewTypeScriptExtractor().Extract("plain.ts",
		[]byte("export function add(a: number, b: number) { return a + b }\nconst x = () => 42\n"))
	require.NoError(t, err)
	for _, n := range res.Nodes {
		_, has := n.Meta["ui_component"]
		assert.False(t, has, "no node in a JSX-free file should carry ui_component")
	}
}

// TestReactDetect_JS covers the JavaScript extractor parity for a
// function component and a class component.
func TestReactDetect_JS(t *testing.T) {
	src := []byte(`import React from 'react'

function Card() { return <div/> }
class Widget extends React.Component { render() { return <span/> } }
`)
	res, err := NewJavaScriptExtractor().Extract("App.jsx", src)
	require.NoError(t, err)
	assert.Equal(t, "react", uiComp(res.Nodes, "Card"))
	assert.Equal(t, "react", uiComp(res.Nodes, "Widget"))
	assert.Equal(t, "class", componentKind(res.Nodes, "Widget"))
}
