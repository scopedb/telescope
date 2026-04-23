package semantic

import "testing"

func TestDefaultRegistryValidate(t *testing.T) {
	if err := Default.Validate(); err != nil {
		t.Fatalf("default registry must validate: %v", err)
	}
}
