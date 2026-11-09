package identitybrand

import (
	"encoding/json"
	"os"
)

// Deployment creates private brand input only on first provisioning. Existing
// inputs and intentionally unbranded installations retain their choice.
func Deployment(path, variant, console, automatic string, existing bool) ([]byte, error) {
	switch automatic {
	case "", "auto", "on", "off":
	default:
		return nil, ErrInvalid
	}
	if !existing && console != "off" && automatic != "off" {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			name := "Voptrack"
			switch variant {
			case "household":
				name = "Voptrack Home"
			case "research":
				name = "Voptrack Research"
			case "", "standard":
			default:
				return nil, ErrInvalid
			}
			palette := Palette{Accent: "#0C6A61", Surface: "#F2F2F7", Text: "#111418"}
			raw, err := json.Marshal(Settings{Name: name, Light: palette, Dark: palette})
			if err != nil {
				return nil, err
			}
			f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return nil, err
			}
			_, err = f.Write(raw)
			closed := f.Close()
			if err != nil {
				return nil, err
			}
			if closed != nil {
				return nil, closed
			}
		} else if err != nil {
			return nil, err
		}
	}
	return Fragment(path)
}
