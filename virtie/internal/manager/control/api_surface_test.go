package control

import (
	"reflect"
	"testing"
)

func TestControlDispatchAndServerWiringStayPrivate(t *testing.T) {
	if _, ok := reflect.TypeOf(&Router{}).MethodByName("Handle"); ok {
		t.Fatal("Router.Handle exposes requestEnvelope and responseEnvelope; keep dispatch private")
	}
	routerType := reflect.TypeOf(Router{})
	for _, name := range []string{"Core", "Suspend", "Hotplug", "Balloon"} {
		if _, ok := routerType.FieldByName(name); ok {
			t.Fatalf("Router.%s should not be exported wiring", name)
		}
	}
	serverType := reflect.TypeOf(Server{})
	for _, name := range []string{"Handler", "Logger"} {
		if _, ok := serverType.FieldByName(name); ok {
			t.Fatalf("Server.%s should not be exported wiring", name)
		}
	}
}
