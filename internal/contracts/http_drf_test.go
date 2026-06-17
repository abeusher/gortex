package contracts

import (
	"testing"

	"github.com/zzet/gortex/internal/graph"
)

func TestDRF_RouterRegisterViewSetActions(t *testing.T) {
	src := []byte(`from rest_framework import viewsets
from rest_framework.routers import DefaultRouter

class UserViewSet(viewsets.ModelViewSet):
    def list(self, request):
        return Response()
    def retrieve(self, request, pk=None):
        return Response()

router = DefaultRouter()
router.register(r'users', UserViewSet, basename='user')
`)
	nodes := []*graph.Node{
		flaskNode("app.py::UserViewSet", "UserViewSet", graph.KindType, 4),
		flaskNode("app.py::UserViewSet.list", "list", graph.KindMethod, 5),
		flaskNode("app.py::UserViewSet.retrieve", "retrieve", graph.KindMethod, 7),
	}
	cs := (&HTTPExtractor{}).Extract("app.py", src, nodes, nil)

	// Collection actions on /users.
	list := flaskFind(cs, "GET", "/users")
	if list == nil {
		t.Fatalf("expected GET /users (list), got %+v", contractPaths(cs))
	}
	if list.Meta["framework"] != "drf" || list.Meta["drf_action"] != "list" {
		t.Errorf("list meta = %+v", list.Meta)
	}
	if list.SymbolID != "app.py::UserViewSet.list" {
		t.Errorf("list SymbolID = %q, want the explicit action method", list.SymbolID)
	}
	create := flaskFind(cs, "POST", "/users")
	if create == nil {
		t.Fatalf("expected POST /users (create), got %+v", contractPaths(cs))
	}
	// create is inherited from ModelViewSet → resolves to the ViewSet class.
	if create.SymbolID != "app.py::UserViewSet" {
		t.Errorf("create SymbolID = %q, want the ViewSet class (inherited)", create.SymbolID)
	}

	// Detail actions on /users/{pk}.
	retrieve := flaskFind(cs, "GET", "/users/{p1}")
	if retrieve == nil {
		t.Fatalf("expected GET /users/{p1} (retrieve), got %+v", contractPaths(cs))
	}
	if retrieve.SymbolID != "app.py::UserViewSet.retrieve" {
		t.Errorf("retrieve SymbolID = %q, want the explicit action method", retrieve.SymbolID)
	}
	for _, m := range []string{"PUT", "PATCH", "DELETE"} {
		if flaskFind(cs, m, "/users/{p1}") == nil {
			t.Errorf("expected %s /users/{p1} from ModelViewSet, got %+v", m, contractPaths(cs))
		}
	}
}

func TestDRF_ReadOnlyViewSetOnlyListAndRetrieve(t *testing.T) {
	src := []byte(`from rest_framework import viewsets

class TagViewSet(viewsets.ReadOnlyModelViewSet):
    queryset = Tag.objects.all()

router.register(r'tags', TagViewSet)
`)
	nodes := []*graph.Node{
		flaskNode("app.py::TagViewSet", "TagViewSet", graph.KindType, 3),
	}
	cs := (&HTTPExtractor{}).Extract("app.py", src, nodes, nil)

	if flaskFind(cs, "GET", "/tags") == nil {
		t.Errorf("expected GET /tags (list), got %+v", contractPaths(cs))
	}
	if flaskFind(cs, "GET", "/tags/{p1}") == nil {
		t.Errorf("expected GET /tags/{p1} (retrieve), got %+v", contractPaths(cs))
	}
	// A read-only ViewSet exposes no write actions.
	for _, m := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if flaskFind(cs, m, "/tags") != nil || flaskFind(cs, m, "/tags/{p1}") != nil {
			t.Errorf("read-only ViewSet should not expose %s, got %+v", m, contractPaths(cs))
		}
	}
}
