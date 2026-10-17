package youtube

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nazboyko/channel-tongues/internal/store"
)

// Backup is the state of a video's title, description and translations taken
// right before a write, plus what was about to be written.
type Backup struct {
	ID            string                  `json:"id"`
	VideoID       string                  `json:"videoId"`
	ChannelID     string                  `json:"channelId,omitempty"`
	TakenAt       time.Time               `json:"takenAt"`
	Reason        string                  `json:"reason"` // publish, restore, set-default-language
	JobID         string                  `json:"jobId,omitempty"`
	Snippet       json.RawMessage         `json:"snippet"`
	Localizations map[string]Localization `json:"localizations"`
	Planned       map[string]Localization `json:"planned,omitempty"`
	// History is what Channel Tongues had written to the video before this
	// write, so an undo can give those languages back to it.
	History map[string]store.HistoryEntry `json:"history,omitempty"`
}

// BackupInfo is one line of a backup listing.
type BackupInfo struct {
	ID        string    `json:"id"`
	TakenAt   time.Time `json:"takenAt"`
	Reason    string    `json:"reason"`
	Languages int       `json:"languages"`
}

var backupIDPattern = regexp.MustCompile(`^\d{8}-\d{6}(-\d+)?$`)

// Backups writes and reads backup files under <dir>/<videoId>/<id>.json.
type Backups struct {
	Dir string
	Now func() time.Time
}

func (b *Backups) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

func (b *Backups) videoDir(videoID string) (string, error) {
	if !idPattern.MatchString(videoID) {
		return "", fmt.Errorf("%q is not a video id", videoID)
	}
	return filepath.Join(b.Dir, videoID), nil
}

// Write stores a backup and returns its id (a UTC timestamp).
func (b *Backups) Write(bk Backup) (string, error) {
	dir, err := b.videoDir(bk.VideoID)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if bk.TakenAt.IsZero() {
		bk.TakenAt = b.now().UTC()
	}
	base := bk.TakenAt.UTC().Format("20060102-150405")
	if bk.Localizations == nil {
		bk.Localizations = map[string]Localization{}
	}
	for n := 1; ; n++ {
		id := base
		if n > 1 {
			id = fmt.Sprintf("%s-%d", base, n)
		}
		bk.ID = id
		data, err := json.MarshalIndent(bk, "", "  ")
		if err != nil {
			return "", err
		}
		f, err := os.OpenFile(filepath.Join(dir, id+".json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			f.Close()
			return "", err
		}
		if err := f.Close(); err != nil {
			return "", err
		}
		return id, nil
	}
}

// Read loads one backup.
func (b *Backups) Read(videoID, id string) (*Backup, error) {
	dir, err := b.videoDir(videoID)
	if err != nil {
		return nil, err
	}
	id = strings.TrimSuffix(id, ".json")
	if !backupIDPattern.MatchString(id) {
		return nil, fmt.Errorf("%q is not a backup name; run tongues backups %s to see them", id, videoID)
	}
	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("there is no backup %s for video %s; run tongues backups %s to see them", id, videoID, videoID)
	}
	if err != nil {
		return nil, err
	}
	var bk Backup
	if err := json.Unmarshal(data, &bk); err != nil {
		return nil, fmt.Errorf("backup %s is damaged: %w", id, err)
	}
	if bk.VideoID != videoID {
		return nil, fmt.Errorf("backup %s belongs to video %s, not %s", id, bk.VideoID, videoID)
	}
	return &bk, nil
}

// List returns a video's backups, newest first.
func (b *Backups) List(videoID string) ([]BackupInfo, error) {
	dir, err := b.videoDir(videoID)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []BackupInfo
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !backupIDPattern.MatchString(id) {
			continue
		}
		bk, err := b.Read(videoID, id)
		if err != nil {
			continue
		}
		var snip struct {
			DefaultLanguage string `json:"defaultLanguage"`
		}
		json.Unmarshal(bk.Snippet, &snip)
		n := len((&Video{DefaultLanguage: snip.DefaultLanguage, Localizations: bk.Localizations}).Languages())
		out = append(out, BackupInfo{ID: id, TakenAt: bk.TakenAt, Reason: bk.Reason, Languages: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].TakenAt.Equal(out[j].TakenAt) {
			return out[i].TakenAt.After(out[j].TakenAt)
		}
		return suffixNumber(out[i].ID) > suffixNumber(out[j].ID)
	})
	return out, nil
}

// suffixNumber is the n of an id ending in -n (1 when there is none).
func suffixNumber(id string) int {
	parts := strings.Split(id, "-")
	if len(parts) == 3 {
		n, _ := strconv.Atoi(parts[2])
		return n
	}
	return 1
}
