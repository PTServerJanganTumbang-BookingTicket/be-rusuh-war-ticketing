package valueobject

import "testing"

func TestLookup(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		wantOK    bool
		wantName  string
		wantPrice int64
	}{
		{name: "vip", id: "cat_vip", wantOK: true, wantName: "VIP", wantPrice: 1500000},
		{name: "festival", id: "cat_festival", wantOK: true, wantName: "Festival", wantPrice: 850000},
		{name: "tribune", id: "cat_tribune", wantOK: true, wantName: "Tribune", wantPrice: 500000},
		{name: "unknown", id: "cat_nope", wantOK: false},
		{name: "empty", id: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Lookup(tt.id)
			if ok != tt.wantOK {
				t.Fatalf("Lookup(%q) ok = %v, want %v", tt.id, ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got.ID != tt.id {
				t.Errorf("ID = %q, want %q", got.ID, tt.id)
			}
			if got.Name != tt.wantName {
				t.Errorf("Name = %q, want %q", got.Name, tt.wantName)
			}
			if got.Price != tt.wantPrice {
				t.Errorf("Price = %d, want %d", got.Price, tt.wantPrice)
			}
		})
	}
}
