package catalogue

import "testing"

func TestPinnedExportIntegrity(t *testing.T) {
	c, e := Load()
	if e != nil {
		t.Fatal(e)
	}
	if len(c.Documents) != 80 || c.SourceRevision != "30440aea0bf5f0cc95f4110b437c9e8ca83009b6" {
		t.Fatal("wrong app export")
	}
	tracks := 0
	for _, d := range c.Documents {
		if d.Kind == "track" {
			tracks++
		}
	}
	if tracks != 72 || !c.Internal["com.voptrack.home_copy"] {
		t.Fatal("catalogue track/internal classification lost")
	}
}
