package huburl

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantWS   string
		wantHTTP string
	}{
		{
			name:     "http base",
			input:    "http://localhost:8080",
			wantWS:   "ws://localhost:8080/ws/runtime",
			wantHTTP: "http://localhost:8080",
		},
		{
			name:     "bare host",
			input:    "localhost:8090",
			wantWS:   "ws://localhost:8090/ws/runtime",
			wantHTTP: "http://localhost:8090",
		},
		{
			name:     "ws runtime url",
			input:    "ws://localhost:8080/ws/runtime",
			wantWS:   "ws://localhost:8080/ws/runtime",
			wantHTTP: "http://localhost:8080",
		},
		{
			name:     "https subpath",
			input:    "https://hub.example.com/amurg",
			wantWS:   "wss://hub.example.com/amurg/ws/runtime",
			wantHTTP: "https://hub.example.com/amurg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.input, err)
			}
			if got.WSURL != tt.wantWS {
				t.Fatalf("WSURL = %q, want %q", got.WSURL, tt.wantWS)
			}
			if got.HTTPBase != tt.wantHTTP {
				t.Fatalf("HTTPBase = %q, want %q", got.HTTPBase, tt.wantHTTP)
			}
			if got.DisplayURL != tt.wantHTTP {
				t.Fatalf("DisplayURL = %q, want %q", got.DisplayURL, tt.wantHTTP)
			}
		})
	}
}

func TestParseRejectsQuery(t *testing.T) {
	if _, err := Parse("http://localhost:8080?x=1"); err == nil {
		t.Fatal("expected error for query string, got nil")
	}
}

func TestCloud(t *testing.T) {
	got := Cloud()
	if got.WSURL != "wss://hub.amurg.ai/ws/runtime" {
		t.Fatalf("WSURL = %q", got.WSURL)
	}
	if got.HTTPBase != "https://hub.amurg.ai" {
		t.Fatalf("HTTPBase = %q", got.HTTPBase)
	}
}
