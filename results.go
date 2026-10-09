package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (r *runner) save(run *runResult) error {
	r.writing.Lock()
	defer r.writing.Unlock()
	r.mu.Lock()
	body, _ := json.Marshal(run)
	r.mu.Unlock()
	err := writeFileAtomically(r.config.resultsDir, run.ID, body)
	if err != nil {
		r.log.Printf("run %s: the run result was not written: %v", run.ID, err)
	}
	return err
}

func writeFileAtomically(dir, id string, body []byte) error {
	file, err := os.CreateTemp(dir, id+".*.tmp")
	if err != nil {
		return err
	}
	_, err = file.Write(body)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(file.Name(), filepath.Join(dir, id+".json"))
	}
	if err != nil {
		os.Remove(file.Name())
	}
	return err
}

func (r *runner) load(id string) *runResult {
	if _, ok := runIDTime(id); !ok {
		return nil
	}
	text, err := os.ReadFile(filepath.Join(r.config.resultsDir, id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var run runResult
	if err == nil {
		err = json.Unmarshal(text, &run)
	}
	if err != nil {
		r.log.Printf("run %s: the run result was not read: %v", id, err)
		return nil
	}
	return &run
}

func (r *runner) markInterrupted() {
	entries, err := os.ReadDir(r.config.resultsDir)
	if err != nil {
		r.log.Printf("results: %v", err)
		return
	}
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok || entry.IsDir() {
			continue
		}
		if run := r.load(id); run != nil && !isFinal(run.State) {
			run.State, run.Reason = "error", "runner restarted"
			r.save(run)
		}
	}
}

func (r *runner) sweep() {
	retention, _ := time.ParseDuration(cmp.Or(r.config.retention, "168h"))
	entries, err := os.ReadDir(r.config.resultsDir)
	if err != nil {
		r.log.Printf("results: %v", err)
		return
	}
	for _, entry := range entries {
		id, _, _ := strings.Cut(entry.Name(), ".")
		if at, ok := runIDTime(id); ok && !entry.IsDir() && time.Since(at) > retention {
			if err := os.Remove(filepath.Join(r.config.resultsDir, entry.Name())); err != nil {
				r.log.Printf("results: %v", err)
			}
		}
	}
}
