package orgs

import "testing"

func TestValidateSubdomain(t *testing.T) {
	tests := []struct {
		subdomain string
		wantErr   bool
	}{
		{"acme", false},
		{"network", false},
		{"netab12", false},
		{"netab12cde", false},
		{"os43nypz", false},
		{"netab12cd", true},
		{"net123456", true},
		{"abc", true},
		{"console", true},
		{"ac-me", true},
		{"Acme", true},
	}
	for _, tt := range tests {
		t.Run(tt.subdomain, func(t *testing.T) {
			err := ValidateSubdomain(tt.subdomain)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSubdomain(%q) = %v, wantErr %v", tt.subdomain, err, tt.wantErr)
			}
		})
	}
}
