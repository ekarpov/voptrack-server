// voptrack-replicator is host configured; ordinary Console has no remote authority.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	bc "github.com/ekarpov/voptrack-server/internal/backupcollector"
	br "github.com/ekarpov/voptrack-server/internal/backupreplication"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if run() != nil {
		fmt.Fprintln(os.Stderr, "replication unavailable; preserve pending inputs and inspect host configuration")
		os.Exit(1)
	}
}
func run() error {
	config := flag.String("config", "", "private explicit host configuration")
	mode := flag.String("mode", "copy", "copy, list or fetch")
	once := flag.Bool("once", false, "one scan")
	backup := flag.String("backup", "", "selected opaque remote backup descriptor")
	evidence := flag.String("evidence", "", "selected opaque evidence descriptor")
	target := flag.String("target", "", "new private host staging directory (fetch only)")
	kit := flag.String("kit", "", "separately retained committed kit (fetch only)")
	settings := flag.String("settings", "", "explicit genuine settings export when remote companion is absent")
	flag.Parse()
	if flag.NArg() != 0 || *config == "" || (*mode != "copy" && *mode != "list" && *mode != "fetch") {
		return br.ErrUnavailable
	}
	var c br.Config
	if bc.ReadPrivateJSON(*config, &c) != nil {
		return br.ErrUnavailable
	}
	// Only the explicit private credential file is consulted. No AWS environment,
	// profile, metadata or developer credentials are ever discovered.
	objects, e := br.NewS3(c)
	if e != nil {
		return e
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if *mode == "list" {
		// Listing emits opaque selection references and timestamps, never payloads,
		// provider errors, configuration, credentials, paths or personal labels.
		w := br.Worker{Config: c, Objects: objects}
		values, e := w.List(ctx)
		if e != nil {
			return e
		}
		type item struct {
			ID, Kind          string
			Published         time.Time
			SettingsAvailable bool
		}
		safe := []item{}
		for _, v := range values {
			safe = append(safe, item{v.ID, v.Kind, v.Published, v.Settings != nil})
		}
		return json.NewEncoder(os.Stdout).Encode(safe)
	}
	if *mode == "fetch" {
		return br.Fetch(ctx, c, objects, *backup, *evidence, *target, *kit, *settings)
	}
	if *kit != "" || *target != "" || *settings != "" || *backup != "" || *evidence != "" {
		return br.ErrUnavailable
	}
	w, e := br.Open(c, objects)
	if e != nil {
		return e
	}
	defer w.Close()
	for {
		e = w.Scan(ctx)
		if *once {
			return e
		}
		if e != nil {
			fmt.Fprintln(os.Stderr, "replication unavailable; last verified copy and pending inputs retained")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Minute):
		}
	}
}
