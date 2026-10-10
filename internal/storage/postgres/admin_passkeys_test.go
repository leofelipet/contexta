package postgres

import (
	"testing"

	"github.com/leofelipet/contexta/internal/passkeys"
)

func TestValidateAdminPasskey(t *testing.T) {
	t.Parallel()
	valid := passkeys.CreateParams{
		ID: "abcDEF123_-", PublicKey: "cHVibGljS2V5", Counter: 0,
		Transports: []string{"internal", "hybrid", "internal"}, Name: "  iPhone  ",
		DeviceType: "multiDevice",
	}
	got, err := validateAdminPasskey(valid)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "iPhone" || len(got.Transports) != 2 || got.Transports[0] != "internal" {
		t.Fatalf("got = %#v", got)
	}

	cases := []passkeys.CreateParams{
		{ID: "", PublicKey: "abc"},
		{ID: "abc", PublicKey: ""},
		{ID: "abc+", PublicKey: "abc"},
		{ID: "abc", PublicKey: "abc", Counter: -1},
		{ID: "abc", PublicKey: "abc", DeviceType: "unknown"},
		{ID: "abc", PublicKey: "abc", Transports: []string{"carrier-pigeon"}},
		{ID: "abc", PublicKey: "abc", Name: string(make([]byte, 65))},
	}
	for _, params := range cases {
		if _, err := validateAdminPasskey(params); err != ErrInvalidArgument {
			t.Fatalf("params = %#v, err = %v, want invalid argument", params, err)
		}
	}

	emptyName, err := validateAdminPasskey(passkeys.CreateParams{ID: "abc", PublicKey: "abc"})
	if err != nil || emptyName.Name != "Passkey" {
		t.Fatalf("empty name = %#v, err = %v", emptyName, err)
	}
}
