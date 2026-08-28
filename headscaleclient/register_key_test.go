package headscaleclient

import "testing"

func TestNormalizeRegistrationKey(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "already prefixed",
			input: "hskey-authreq-abcdefghijklmnopqrstuvwx",
			want:  "hskey-authreq-abcdefghijklmnopqrstuvwx",
		},
		{
			name:  "bare token gets prefixed",
			input: "abcdefghijklmnopqrstuvwx",
			want:  "hskey-authreq-abcdefghijklmnopqrstuvwx",
		},
		{
			name:  "full registration URL",
			input: "https://hs.example.test/register/abcdefghijklmnopqrstuvwx",
			want:  "hskey-authreq-abcdefghijklmnopqrstuvwx",
		},
		{
			name:  "full registration URL with already-prefixed token",
			input: "https://hs.example.test/register/hskey-authreq-abcdefghijklmnopqrstuvwx",
			want:  "hskey-authreq-abcdefghijklmnopqrstuvwx",
		},
		{
			name:  "legacy mkey format",
			input: "mkey:abcdefghijklmnopqrstuvwx",
			want:  "mkey:abcdefghijklmnopqrstuvwx",
		},
		{
			name:  "leading/trailing whitespace trimmed",
			input: "  hskey-authreq-abcdefghijklmnopqrstuvwx  ",
			want:  "hskey-authreq-abcdefghijklmnopqrstuvwx",
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
		{
			name:    "too short to be a real token",
			input:   "short",
			wantErr: true,
		},
		{
			name:    "garbage",
			input:   "not a key at all!!",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeRegistrationKey(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeRegistrationKey(%q) = %q, want error", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeRegistrationKey(%q) unexpected error: %v", tt.input, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeRegistrationKey(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
