package companies

import "testing"

func TestNormalizeLinks(t *testing.T) {
	t.Parallel()
	ok, err := NormalizeLinks([]Link{
		{Label: " Planilha ", URL: " https://docs.google.com/spreadsheets/d/x "},
		{Label: "", URL: ""},
	})
	if err != nil || len(ok) != 1 || ok[0].Label != "Planilha" || ok[0].URL != "https://docs.google.com/spreadsheets/d/x" {
		t.Fatalf("ok = %#v, err = %v", ok, err)
	}
	for _, links := range [][]Link{
		{{Label: "x", URL: "javascript:alert(1)"}},
		{{Label: "x", URL: "/relative"}},
		{{Label: "", URL: "https://example.com"}},
		make([]Link, MaxLinks+1),
	} {
		if _, err := NormalizeLinks(links); err == nil {
			t.Fatalf("links = %#v, err = %v, want invalid", links, err)
		}
	}
}
