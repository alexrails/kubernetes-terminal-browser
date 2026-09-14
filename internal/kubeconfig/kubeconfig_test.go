package kubeconfig

import (
	"testing"
)

func TestParseModesAndNamespace(t *testing.T) {
	data := []byte(`{"current-context":"b","contexts":[{"name":"b","context":{"user":"u"}},{"name":"a","context":{"namespace":"specific","user":"v"}}],"users":[{"name":"u","user":{"exec":{"interactiveMode":"Always","command":"secret"}}}]}`)
	s, e := Parse(data)
	if e != nil {
		t.Fatal(e)
	}
	if s.Current != "b" || len(s.Contexts) != 2 || s.Contexts[0].Name != "a" || s.Contexts[1].Namespace != "default" || s.Contexts[1].InteractiveMode != "Always" {
		t.Fatalf("got %+v", s)
	}
}
