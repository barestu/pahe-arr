package web

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http"
	"os"
	"strconv"

	"pahe-arr/internal/jobs"
)

// client implements the subset of SABnzbd used for Sonarr's upload and completed download handling.
// Only descriptors signed by this instance can enter the queue.
func (b sonarrBridge) client(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	mode := q.Get("mode")
	if (mode == "addfile" && r.Method != "POST") || (mode != "addfile" && r.Method != "GET") {
		http.Error(w, "unsupported method", 405)
		return
	}
	set := b.s.Settings.Get()
	var result any
	switch mode {
	case "version":
		result = map[string]string{"version": "4.3.0"}
	case "get_config":
		result = map[string]any{"config": map[string]any{
			"misc":       map[string]any{"complete_dir": set.OutputDir, "history_retention": "0"},
			"sorters":    []any{},
			"categories": []any{map[string]string{"name": "pahe", "dir": ""}},
		}}
	case "fullstatus":
		result = map[string]any{"status": map[string]string{"completedir": set.OutputDir}}
	case "addfile":
		if q.Get("cat") != "pahe" {
			http.Error(w, "use the pahe category", 400)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		file, _, err := r.FormFile("name")
		if err != nil {
			http.Error(w, "expected an AnimePahe NZB descriptor", 400)
			return
		}
		defer file.Close()
		var doc descriptor
		if err := xml.NewDecoder(file).Decode(&doc); err != nil {
			http.Error(w, "invalid descriptor", 400)
			return
		}
		v, ok := b.unpack(doc.Token)
		if !ok {
			http.Error(w, "descriptor was not issued by this indexer", 400)
			return
		}
		id, err := b.s.Jobs.AddRelease(v.job())
		if err != nil {
			http.Error(w, "saving download failed", 500)
			return
		}
		result = map[string]any{"status": true, "nzo_ids": []string{id}}
	case "queue", "history", "retry":
		if mode == "retry" || q.Get("name") == "delete" {
			id := q.Get("value")
			for _, j := range b.s.Jobs.List() {
				if j.ID == id && j.ReleaseTitle != "" {
					if mode == "retry" {
						b.s.Jobs.Retry(id)
					} else {
						b.s.Jobs.RemoveRelease(id)
					}
					break
				}
			}
			result = map[string]any{"status": true, "nzo_id": id}
			break
		}
		slots := []map[string]any{}
		for _, j := range b.s.Jobs.List() {
			if j.ReleaseTitle == "" || (mode == "history") != j.Status.Finished() || (q.Get("category") != "" && q.Get("category") != "pahe") {
				continue
			}
			slot := map[string]any{"nzo_id": j.ID, "cat": "pahe"}
			if mode == "history" {
				status := "Failed"
				if j.Status == jobs.Done {
					status = "Completed"
				}
				slot["category"] = "pahe"
				slot["status"], slot["name"], slot["storage"], slot["fail_message"] = status, j.ReleaseTitle, j.Output, j.Error
				if stat, err := os.Stat(j.Output); err == nil {
					slot["bytes"] = stat.Size()
				}
			} else {
				status := "Downloading"
				if j.Status == jobs.Queued {
					status = "Queued"
				}
				slot["status"], slot["filename"], slot["percentage"] = status, j.ReleaseTitle, fmt.Sprint(j.Percent())
			}
			slots = append(slots, slot)
		}
		start, _ := strconv.Atoi(q.Get("start"))
		limit, _ := strconv.Atoi(q.Get("limit"))
		start = max(0, min(start, len(slots)))
		total := len(slots)
		slots = slots[start:]
		if limit > 0 && limit < len(slots) {
			slots = slots[:limit]
		}
		result = map[string]any{mode: map[string]any{"slots": slots, "noofslots": total, "paused": false}}
	default:
		http.Error(w, "unsupported client operation", 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}
