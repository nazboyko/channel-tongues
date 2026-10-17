package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	yt "google.golang.org/api/youtube/v3"

	"github.com/nazboyko/channel-tongues/internal/langs"
	"github.com/nazboyko/channel-tongues/internal/store"
)

// Outcome of one language in a publish.
type Outcome string

const (
	OutcomeAdded    Outcome = "added"    // the video had no translation for it
	OutcomeUpdated  Outcome = "updated"  // ours from last time, replaced with the new one
	OutcomeReplaced Outcome = "replaced" // someone else's, replaced because the person asked
	OutcomeKept     Outcome = "kept"     // someone else's, kept as it is
	OutcomeSame     Outcome = "same"     // already exactly this text
	OutcomeFailed   Outcome = "failed"   // the read-back did not show it
	OutcomeRemoved  Outcome = "removed"  // a restore took it away again
)

// Change is the plan (and later the result) for one language.
type Change struct {
	Lang    string  `json:"lang"`
	Key     string  `json:"key"` // the key used on the video (may be an alias such as pt)
	Outcome Outcome `json:"outcome"`
}

// Plan is a complete merged translation map plus what changes per language.
type Plan struct {
	VideoID  string                  `json:"videoId"`
	Existing map[string]Localization `json:"-"`
	Merged   map[string]Localization `json:"-"`
	Changes  []Change                `json:"changes"`
}

// Writes reports whether the plan changes anything on the video.
func (p *Plan) Writes() bool {
	for _, c := range p.Changes {
		switch c.Outcome {
		case OutcomeAdded, OutcomeUpdated, OutcomeReplaced:
			return true
		}
	}
	return false
}

// PlanMerge decides, per language, what to write. It starts from everything
// already on the video and writes ours where the video has none, where the
// current text is the one we wrote last time, or where the person asked to
// replace it. Everything else is kept. The result is the complete map to
// send, because YouTube deletes any language left out of an update.
func PlanMerge(videoID string, existing map[string]Localization, history map[string]store.HistoryEntry, ours map[string]Localization, replace map[string]bool) *Plan {
	p := &Plan{VideoID: videoID, Existing: existing, Merged: map[string]Localization{}}
	for k, v := range existing {
		p.Merged[k] = v
	}
	codes := make([]string, 0, len(ours))
	for code := range ours {
		codes = append(codes, code)
	}
	codes = langs.Sort(codes)
	for _, code := range codes {
		want := ours[code]
		key, cur, has := findKey(existing, code)
		if !has {
			key = code
		}
		ch := Change{Lang: code, Key: key}
		switch {
		case !has:
			ch.Outcome = OutcomeAdded
		case cur == want:
			ch.Outcome = OutcomeSame
		case wroteLastTime(history, key, code, cur):
			ch.Outcome = OutcomeUpdated
		case replace[code] || replace[key]:
			ch.Outcome = OutcomeReplaced
		default:
			ch.Outcome = OutcomeKept
		}
		if ch.Outcome == OutcomeAdded || ch.Outcome == OutcomeUpdated || ch.Outcome == OutcomeReplaced {
			p.Merged[key] = want
		}
		p.Changes = append(p.Changes, ch)
	}
	return p
}

// findKey finds the video's key for a language, allowing aliases (pt for pt-BR).
func findKey(existing map[string]Localization, code string) (string, Localization, bool) {
	if l, ok := existing[code]; ok {
		return code, l, true
	}
	want := langs.Canonical(code)
	keys := make([]string, 0, len(existing))
	for k := range existing {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.EqualFold(langs.Canonical(k), want) {
			return k, existing[k], true
		}
	}
	return "", Localization{}, false
}

// wroteLastTime: the text on the video is exactly what we wrote last time, so
// nobody has edited it since and it is ours to update.
func wroteLastTime(history map[string]store.HistoryEntry, key, code string, cur Localization) bool {
	for _, k := range []string{key, code} {
		if h, ok := history[k]; ok && h.Title == cur.Title && h.Description == cur.Description {
			return true
		}
	}
	return false
}

// Writer performs guarded writes: backup before, read-back after, history
// and the quota ledger recorded.
type Writer struct {
	Client  *Client
	Backups *Backups
	Store   *store.Store
	// Allowed refuses writes to videos outside the test list when it is set.
	Allowed func(videoID string) error
	// ReadBackWaits are the pauses between read-back attempts. YouTube can
	// take a few seconds before a read shows a write it just accepted.
	ReadBackWaits []time.Duration
}

var defaultReadBackWaits = []time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 4 * time.Second}

// readBack reads the translations until done reports true or the waits run
// out (1 unit per read). It returns the last read.
func (w *Writer) readBack(ctx context.Context, videoID string, done func(map[string]Localization) bool) (map[string]Localization, error) {
	waits := w.ReadBackWaits
	if waits == nil {
		waits = defaultReadBackWaits
	}
	var last map[string]Localization
	for i := 0; ; i++ {
		got, err := w.Client.GetLocalizations(ctx, videoID)
		if err != nil {
			return nil, err
		}
		last = got
		if done(got) || i >= len(waits) {
			return last, nil
		}
		select {
		case <-time.After(waits[i]):
		case <-ctx.Done():
			return last, ctx.Err()
		}
	}
}

// PublishResult is what happened, per language.
type PublishResult struct {
	VideoID  string   `json:"videoId"`
	BackupID string   `json:"backupId"`
	Changes  []Change `json:"changes"`
	Wrote    bool     `json:"wrote"`
}

// ErrVideoChanged means the video's translations changed between reading and
// writing (another tab, or the command line, published at the same time).
var ErrVideoChanged = errors.New("the video's translations changed while you were working; nothing was written, try again")

// ErrNoDefaultLanguage is returned when the video has no language set for its
// title and description; YouTube refuses translations then.
var ErrNoDefaultLanguage = errors.New("this video has no language set for its title and description. In Studio open the video, then Details > Show more > Title and description language, pick it and save")

// Prepare reads the video and plans the merge without writing (1 unit).
func (w *Writer) Prepare(ctx context.Context, videoID string, ours map[string]Localization, replace map[string]bool) (*Video, *Plan, error) {
	v, err := w.Client.GetVideo(ctx, videoID)
	if err != nil {
		return nil, nil, err
	}
	return w.PrepareFrom(v, ours, replace)
}

// PrepareFrom plans the merge from a video that was just read.
func (w *Writer) PrepareFrom(v *Video, ours map[string]Localization, replace map[string]bool) (*Video, *Plan, error) {
	videoID := v.ID
	if v.DefaultLanguage == "" {
		return v, nil, ErrNoDefaultLanguage
	}
	clean := map[string]Localization{}
	for code, l := range ours {
		if langs.SameBase(code, v.DefaultLanguage) {
			continue // never write the video's own language as a translation
		}
		clean[code] = l
	}
	history, err := w.Store.History(videoID)
	if err != nil {
		return nil, nil, err
	}
	return v, PlanMerge(videoID, v.Localizations, history, clean, replace), nil
}

// Publish writes the plan: backup, update with the complete map, read back,
// record history. It sends only the translations part of the video, never
// its title, description or tags.
func (w *Writer) Publish(ctx context.Context, v *Video, plan *Plan, jobID string) (*PublishResult, error) {
	res := &PublishResult{VideoID: v.ID, Changes: plan.Changes}
	if !plan.Writes() {
		return res, nil
	}
	if w.Allowed != nil {
		if err := w.Allowed(v.ID); err != nil {
			return nil, err
		}
	}
	planned := map[string]Localization{}
	for _, c := range plan.Changes {
		if c.Outcome == OutcomeAdded || c.Outcome == OutcomeUpdated || c.Outcome == OutcomeReplaced {
			planned[c.Key] = plan.Merged[c.Key]
		}
	}
	// Read again right before writing: if anything changed since the plan was
	// made, sending the plan's complete map would delete those changes.
	fresh, err := w.Client.GetLocalizations(ctx, v.ID)
	if err != nil {
		return nil, err
	}
	if !equalMaps(fresh, plan.Existing) {
		return nil, ErrVideoChanged
	}
	history, err := w.Store.History(v.ID)
	if err != nil {
		return nil, err
	}
	backupID, err := w.Backups.Write(Backup{
		VideoID: v.ID, ChannelID: v.ChannelID, Reason: "publish", JobID: jobID,
		Snippet: v.Snippet, Localizations: v.Localizations, Planned: planned, History: history,
	})
	if err != nil {
		return nil, fmt.Errorf("could not write the backup, so nothing was published: %w", err)
	}
	res.BackupID = backupID

	if err := w.Client.UpdateLocalizations(ctx, v.ID, plan.Merged); err != nil {
		return res, err
	}
	res.Wrote = true

	after, err := w.readBack(ctx, v.ID, func(got map[string]Localization) bool {
		for k := range planned {
			if got[k] != plan.Merged[k] {
				return false
			}
		}
		return true
	})
	if err != nil {
		return res, fmt.Errorf("published, but reading it back failed: %w", err)
	}
	for i, c := range res.Changes {
		switch c.Outcome {
		case OutcomeAdded, OutcomeUpdated, OutcomeReplaced, OutcomeSame:
			if got, ok := after[c.Key]; !ok || got != plan.Merged[c.Key] {
				if c.Outcome != OutcomeSame {
					res.Changes[i].Outcome = OutcomeFailed
				}
				continue
			}
			l := plan.Merged[c.Key]
			if err := w.Store.RecordHistory(v.ID, store.HistoryEntry{Lang: c.Key, Title: l.Title, Description: l.Description, BackupID: backupID}); err != nil {
				return res, err
			}
		}
	}
	return res, nil
}

// Restore undoes the write a backup was taken for: each language that write
// set goes back to its value in the backup (or is removed if the video did
// not have it), and everything else on the video stays as it is now. It runs
// behind the same guards: a backup of the current state first, a read-back
// after.
func (w *Writer) Restore(ctx context.Context, videoID, backupID string) (*PublishResult, error) {
	bk, err := w.Backups.Read(videoID, backupID)
	if err != nil {
		return nil, err
	}
	if w.Allowed != nil {
		if err := w.Allowed(videoID); err != nil {
			return nil, err
		}
	}
	v, err := w.Client.GetVideo(ctx, videoID)
	if err != nil {
		return nil, err
	}
	before := bk.Localizations
	if before == nil {
		before = map[string]Localization{}
	}
	target := map[string]Localization{}
	touched := map[string]bool{}
	if len(bk.Planned) > 0 {
		for k, l := range v.Localizations {
			target[k] = l
		}
		for key := range bk.Planned {
			touched[key] = true
			if l, had := before[key]; had {
				target[key] = l
			} else {
				delete(target, key)
			}
		}
	} else {
		// A backup without a plan (taken before a language setting change):
		// put the whole map back.
		for k, l := range before {
			target[k] = l
		}
		for _, k := range unionKeys(v.Localizations, before) {
			touched[k] = true
		}
	}
	res := &PublishResult{VideoID: videoID}
	for _, code := range unionKeys(v.Localizations, target) {
		cur, had := v.Localizations[code]
		want, keep := target[code]
		switch {
		case !keep:
			res.Changes = append(res.Changes, Change{Lang: code, Key: code, Outcome: OutcomeRemoved})
		case !had:
			res.Changes = append(res.Changes, Change{Lang: code, Key: code, Outcome: OutcomeAdded})
		case cur != want:
			res.Changes = append(res.Changes, Change{Lang: code, Key: code, Outcome: OutcomeUpdated})
		default:
			res.Changes = append(res.Changes, Change{Lang: code, Key: code, Outcome: OutcomeSame})
		}
	}
	if equalMaps(v.Localizations, target) {
		return res, nil
	}
	history, err := w.Store.History(videoID)
	if err != nil {
		return nil, err
	}
	newID, err := w.Backups.Write(Backup{
		VideoID: videoID, ChannelID: v.ChannelID, Reason: "restore " + backupID,
		Snippet: v.Snippet, Localizations: v.Localizations, Planned: changedKeys(v.Localizations, target), History: history,
	})
	if err != nil {
		return nil, fmt.Errorf("could not write the backup, so nothing was restored: %w", err)
	}
	res.BackupID = newID
	if err := w.Client.UpdateLocalizations(ctx, videoID, target); err != nil {
		return res, err
	}
	res.Wrote = true
	after, err := w.readBack(ctx, videoID, func(got map[string]Localization) bool { return equalMaps(got, target) })
	if err != nil {
		return res, fmt.Errorf("restored, but reading it back failed: %w", err)
	}
	for i := range res.Changes {
		c := res.Changes[i]
		got, has := after[c.Key]
		want, keep := target[c.Key]
		if has != keep || got != want {
			res.Changes[i].Outcome = OutcomeFailed
		}
	}
	// History follows the text on the video: what we wrote before the undone
	// publish is ours again; anything else is forgotten.
	for lang := range touched {
		l, ok := after[lang]
		switch snap, inSnap := bk.History[lang]; {
		case ok && inSnap && snap.Title == l.Title && snap.Description == l.Description:
			w.Store.RecordHistory(videoID, snap)
		default:
			w.Store.ForgetHistory(videoID, lang)
		}
	}
	return res, nil
}

// changedKeys lists the languages whose value differs between two maps, with
// the new value (or an empty one for a removal), as a backup's plan.
func changedKeys(from, to map[string]Localization) map[string]Localization {
	out := map[string]Localization{}
	for _, k := range unionKeys(from, to) {
		a, inA := from[k]
		b, inB := to[k]
		if inA != inB || a != b {
			out[k] = b
		}
	}
	return out
}

// SetDefaultLanguage sets the language of the title and description. It is
// the only write that touches the snippet: every writable snippet field is
// sent back exactly as read, after a backup.
func (w *Writer) SetDefaultLanguage(ctx context.Context, videoID, lang string) (string, error) {
	if w.Allowed != nil {
		if err := w.Allowed(videoID); err != nil {
			return "", err
		}
	}
	v, err := w.Client.GetVideo(ctx, videoID)
	if err != nil {
		return "", err
	}
	var snip yt.VideoSnippet
	if err := json.Unmarshal(v.Snippet, &snip); err != nil {
		return "", err
	}
	backupID, err := w.Backups.Write(Backup{
		VideoID: videoID, ChannelID: v.ChannelID, Reason: "set-default-language " + lang,
		Snippet: v.Snippet, Localizations: v.Localizations,
	})
	if err != nil {
		return "", fmt.Errorf("could not write the backup, so nothing was changed: %w", err)
	}
	if err := w.Client.UpdateSnippetLanguage(ctx, videoID, &snip, lang); err != nil {
		return backupID, err
	}
	after, err := w.Client.GetVideo(ctx, videoID)
	if err != nil {
		return backupID, err
	}
	if after.DefaultLanguage != lang || after.Title != v.Title || after.Description != v.Description {
		return backupID, fmt.Errorf("the change did not read back as expected; the backup is %s", backupID)
	}
	return backupID, nil
}

func unionKeys(a, b map[string]Localization) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range []map[string]Localization{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return langs.Sort(out)
}

func equalMaps(a, b map[string]Localization) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// UpdateLocalizations sends the complete translation map (50 units). Only the
// localizations part is in the request; the log line says so.
func (c *Client) UpdateLocalizations(ctx context.Context, videoID string, all map[string]Localization) error {
	body := &yt.Video{Id: videoID, Localizations: map[string]yt.VideoLocalization{}, ForceSendFields: []string{"Localizations"}}
	keys := make([]string, 0, len(all))
	for k, l := range all {
		body.Localizations[k] = yt.VideoLocalization{Title: l.Title, Description: l.Description, ForceSendFields: []string{"Title", "Description"}}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	c.log.Info("videos.update", "part", "localizations", "video", videoID, "channel", c.ChannelID, "languages", strings.Join(keys, ","), "snippet", false)
	_, err := c.svc.Videos.Update([]string{"localizations"}, body).Context(ctx).Do()
	c.spend("videos.update", CostUpdate)
	if err != nil {
		return apiError(err)
	}
	return nil
}

// UpdateSnippetLanguage re-sends the writable snippet fields as read, with
// the default language set (50 units).
func (c *Client) UpdateSnippetLanguage(ctx context.Context, videoID string, read *yt.VideoSnippet, lang string) error {
	snip := &yt.VideoSnippet{
		Title:                read.Title,
		Description:          read.Description,
		Tags:                 read.Tags,
		CategoryId:           read.CategoryId,
		DefaultAudioLanguage: read.DefaultAudioLanguage,
		DefaultLanguage:      lang,
		ForceSendFields:      []string{"Title", "Description", "Tags", "CategoryId", "DefaultLanguage"},
	}
	c.log.Info("videos.update", "part", "snippet", "video", videoID, "channel", c.ChannelID, "defaultLanguage", lang, "reason", "set-default-language")
	_, err := c.svc.Videos.Update([]string{"snippet"}, &yt.Video{Id: videoID, Snippet: snip}).Context(ctx).Do()
	c.spend("videos.update", CostUpdate)
	if err != nil {
		return apiError(err)
	}
	return nil
}
