package main

import (
	bc "github.com/ekarpov/voptrack-server/internal/backupcollector"
	"path/filepath"
	"time"
)

func (a *app) offMachineData(d map[string]any) {
	path := ""
	if a.cfg.RuntimeFile != "" {
		path = filepath.Join(filepath.Dir(a.cfg.RuntimeFile), "off-machine.json")
	}
	s, _ := bc.ReadReplicationStatus(path, time.Now())
	d["OffMachine"] = s
}
