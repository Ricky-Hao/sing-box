package option

import (
	"context"
	"testing"

	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
)

func TestIWANEndpointRejectsSystemMode(t *testing.T) {
	ctx := service.ContextWith[EndpointOptionsRegistry](context.Background(), iwanEndpointOptionsRegistry{})
	var options Options
	err := json.UnmarshalContext(ctx, []byte(`{
		"endpoints": [{
			"type": "iwan",
			"server": "127.0.0.1",
			"username": "myuser",
			"password": "mypassword",
			"system": true
		}]
	}`), &options)
	if err == nil {
		t.Fatal("accepted unsupported iWAN system mode")
	}
}

type iwanEndpointOptionsRegistry struct{}

func (iwanEndpointOptionsRegistry) OptionTypes() []string {
	return []string{"iwan"}
}

func (iwanEndpointOptionsRegistry) CreateOptions(endpointType string) (any, bool) {
	if endpointType != "iwan" {
		return nil, false
	}
	return new(IWANEndpointOptions), true
}
