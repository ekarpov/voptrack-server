package identitybrand

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestFreshBrandingAndPreservedOperatorChoice(t *testing.T) {
	for variant, name := range map[string]string{"household": "Voptrack Home", "research": "Voptrack Research", "standard": "Voptrack"} {
		path := filepath.Join(t.TempDir(), "identity-brand.json")
		raw, err := Deployment(path, variant, "on", "auto", false)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Brand struct {
				Name string
				Logo string `json:"logo_file"`
			} `json:"identity_brand"`
		}
		wrapped := append(append([]byte("{"), raw[:len(raw)-1]...), '}')
		if json.Unmarshal(wrapped, &got) != nil || got.Brand.Name != name || got.Brand.Logo != Logo {
			t.Fatal("packaged logo or product identity missing")
		}

		data, _ := os.ReadFile(path)
		var settings Settings
		if json.Unmarshal(data, &settings) != nil || settings.Name != name || settings.Light != settings.Dark {
			t.Fatal("fresh branding incorrect")
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Fatal("brand input is not private")
		}
		prior := string(data)
		_, err = Deployment(path, "other", "on", "off", true)
		data, _ = os.ReadFile(path)
		if err != nil || string(data) != prior {
			t.Fatal("explicit operator brand overwritten")
		}
	}
	for _, tc := range []struct {
		existing      bool
		console, mode string
	}{{true, "on", "auto"}, {false, "off", "auto"}, {false, "on", "off"}} {
		path := filepath.Join(t.TempDir(), "identity-brand.json")
		raw, err := Deployment(path, "household", tc.console, tc.mode, tc.existing)
		if err != nil || len(raw) != 0 {
			t.Fatal("deliberate unbranded setup changed")
		}
		if _, err = os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("unrequested input created")
		}
	}
	path := filepath.Join(t.TempDir(), "identity-brand.json")
	if _, err := Deployment(path, "household", "on", "invalid", false); err == nil {
		t.Fatal("invalid option accepted")
	}
	target := filepath.Join(t.TempDir(), "target")
	if err := os.WriteFile(target, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := Deployment(path, "household", "on", "auto", false); err == nil {
		t.Fatal("symlink accepted")
	}
}
