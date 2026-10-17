package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	yt "google.golang.org/api/youtube/v3"

	"github.com/nazboyko/channel-tongues/internal/langs"
	"github.com/nazboyko/channel-tongues/internal/store"
)

// Localization is one language's title and description on a video.
type Localization struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

// Video is what Channel Tongues needs to know about a video.
type Video struct {
	ID              string                  `json:"id"`
	ChannelID       string                  `json:"channelId"`
	ChannelTitle    string                  `json:"channelTitle"`
	Title           string                  `json:"title"`
	Description     string                  `json:"description"`
	DefaultLanguage string                  `json:"defaultLanguage"`
	PublishedAt     string                  `json:"publishedAt,omitempty"`
	Localizations   map[string]Localization `json:"localizations"`
	// Snippet is the full snippet exactly as read, for backups and the
	// explicit set-default-language command.
	Snippet json.RawMessage `json:"-"`
}

// Languages returns the translations the video already has, most watched
// first. YouTube also lists the video's own language among them; that one is
// not a translation and is left out.
func (v *Video) Languages() []string {
	codes := make([]string, 0, len(v.Localizations))
	for code := range v.Localizations {
		if v.DefaultLanguage != "" && langs.SameBase(code, v.DefaultLanguage) {
			continue
		}
		codes = append(codes, code)
	}
	return langs.Sort(codes)
}

// Has reports whether the video already has a translation for code (any alias).
func (v *Video) Has(code string) bool {
	_, ok := v.Localization(code)
	return ok
}

// Localization finds the translation for code, matching aliases such as pt and pt-BR.
func (v *Video) Localization(code string) (Localization, bool) {
	if l, ok := v.Localizations[code]; ok {
		return l, true
	}
	want := langs.Canonical(code)
	for k, l := range v.Localizations {
		if strings.EqualFold(langs.Canonical(k), want) {
			return l, true
		}
	}
	return Localization{}, false
}

// ErrVideoNotFound means the video does not exist or the signed-in channel cannot see it.
var ErrVideoNotFound = errors.New("video not found")

// Client calls the YouTube Data API and records the cost of every call.
type Client struct {
	svc    *yt.Service
	ledger *Ledger
	store  *store.Store
	log    *slog.Logger
	// ChannelID is the channel whose sign-in this client uses ("" for none).
	ChannelID string
}

// ClientOptions configures NewClient.
type ClientOptions struct {
	HTTP      *http.Client // authorized client; required
	Endpoint  string       // API base URL, for tests and mock mode
	Store     *store.Store // ledger and cache; optional
	Log       *slog.Logger
	ChannelID string
	Now       func() time.Time
}

// NewClient builds a client from an authorized HTTP client.
func NewClient(ctx context.Context, o ClientOptions) (*Client, error) {
	opts := []option.ClientOption{option.WithHTTPClient(o.HTTP)}
	if o.Endpoint != "" {
		opts = append(opts, option.WithEndpoint(strings.TrimRight(o.Endpoint, "/")+"/"))
	}
	svc, err := yt.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	c := &Client{svc: svc, store: o.Store, log: log, ChannelID: o.ChannelID}
	if o.Store != nil {
		c.ledger = &Ledger{Store: o.Store, Now: o.Now}
	}
	return c, nil
}

// Ledger returns the client's quota ledger (nil without a store).
func (c *Client) Ledger() *Ledger { return c.ledger }

func (c *Client) spend(call string, units int) {
	if err := c.ledger.Spend(units); err != nil {
		c.log.Warn("quota ledger", "call", call, "err", err)
	}
}

func videoFromAPI(v *yt.Video) (*Video, error) {
	out := &Video{ID: v.Id, Localizations: map[string]Localization{}}
	if v.Snippet != nil {
		raw, err := json.Marshal(v.Snippet)
		if err != nil {
			return nil, err
		}
		out.Snippet = raw
		out.ChannelID = v.Snippet.ChannelId
		out.ChannelTitle = v.Snippet.ChannelTitle
		out.Title = v.Snippet.Title
		out.Description = v.Snippet.Description
		out.DefaultLanguage = v.Snippet.DefaultLanguage
		out.PublishedAt = v.Snippet.PublishedAt
	}
	for code, l := range v.Localizations {
		out.Localizations[code] = Localization{Title: l.Title, Description: l.Description}
	}
	return out, nil
}

// GetVideo reads a video's snippet and translations (1 unit).
func (c *Client) GetVideo(ctx context.Context, id string) (*Video, error) {
	resp, err := c.svc.Videos.List([]string{"snippet", "localizations"}).Id(id).Context(ctx).Do()
	c.spend("videos.list", CostRead)
	if err != nil {
		return nil, apiError(err)
	}
	if len(resp.Items) == 0 {
		return nil, ErrVideoNotFound
	}
	return videoFromAPI(resp.Items[0])
}

// GetLocalizations reads only a video's translations (1 unit), for read-back.
func (c *Client) GetLocalizations(ctx context.Context, id string) (map[string]Localization, error) {
	resp, err := c.svc.Videos.List([]string{"localizations"}).Id(id).Context(ctx).Do()
	c.spend("videos.list", CostRead)
	if err != nil {
		return nil, apiError(err)
	}
	if len(resp.Items) == 0 {
		return nil, ErrVideoNotFound
	}
	out := map[string]Localization{}
	for code, l := range resp.Items[0].Localizations {
		out[code] = Localization{Title: l.Title, Description: l.Description}
	}
	return out, nil
}

// Channel is a channel's id and name.
type Channel struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// MyChannel returns the channel the sign-in belongs to (1 unit).
func (c *Client) MyChannel(ctx context.Context) (Channel, error) {
	resp, err := c.svc.Channels.List([]string{"snippet"}).Mine(true).Context(ctx).Do()
	c.spend("channels.list", CostRead)
	if err != nil {
		return Channel{}, apiError(err)
	}
	if len(resp.Items) == 0 {
		return Channel{}, errors.New("this Google account has no channel; create one in YouTube first")
	}
	ch := resp.Items[0]
	title := ""
	if ch.Snippet != nil {
		title = ch.Snippet.Title
	}
	return Channel{ID: ch.Id, Title: title}, nil
}

// ListUploads returns a channel's latest uploads, newest first, up to limit
// (all of them when limit <= 0). Cost: 1 for the channel, 1 per 50 playlist
// items, 1 per 50 videos.
func (c *Client) ListUploads(ctx context.Context, channelID string, limit int) ([]*Video, error) {
	chResp, err := c.svc.Channels.List([]string{"contentDetails"}).Id(channelID).Context(ctx).Do()
	c.spend("channels.list", CostRead)
	if err != nil {
		return nil, apiError(err)
	}
	if len(chResp.Items) == 0 || chResp.Items[0].ContentDetails == nil || chResp.Items[0].ContentDetails.RelatedPlaylists == nil {
		return nil, fmt.Errorf("channel %s was not found", channelID)
	}
	uploads := chResp.Items[0].ContentDetails.RelatedPlaylists.Uploads

	var ids []string
	page := ""
	for {
		call := c.svc.PlaylistItems.List([]string{"contentDetails"}).PlaylistId(uploads).MaxResults(50).Context(ctx)
		if page != "" {
			call = call.PageToken(page)
		}
		resp, err := call.Do()
		c.spend("playlistItems.list", CostRead)
		if err != nil {
			var gerr *googleapi.Error
			if errors.As(err, &gerr) && gerr.Code == http.StatusNotFound {
				break // a channel with no uploads has no uploads playlist yet
			}
			return nil, apiError(err)
		}
		for _, it := range resp.Items {
			if it.ContentDetails != nil && it.ContentDetails.VideoId != "" {
				ids = append(ids, it.ContentDetails.VideoId)
			}
		}
		if resp.NextPageToken == "" || (limit > 0 && len(ids) >= limit) {
			break
		}
		page = resp.NextPageToken
	}
	if limit > 0 && len(ids) > limit {
		ids = ids[:limit]
	}

	var out []*Video
	for start := 0; start < len(ids); start += 50 {
		end := min(start+50, len(ids))
		resp, err := c.svc.Videos.List([]string{"snippet", "localizations"}).Id(ids[start:end]...).Context(ctx).Do()
		c.spend("videos.list", CostRead)
		if err != nil {
			return nil, apiError(err)
		}
		byID := map[string]*yt.Video{}
		for _, v := range resp.Items {
			byID[v.Id] = v
		}
		for _, id := range ids[start:end] { // keep playlist order (newest first)
			if v, ok := byID[id]; ok {
				vid, err := videoFromAPI(v)
				if err != nil {
					return nil, err
				}
				out = append(out, vid)
			}
		}
	}
	return out, nil
}

// I18nLanguage is one entry of YouTube's language list.
type I18nLanguage struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

const i18nCacheKey = "i18nLanguages:en"

// I18nLanguages returns YouTube's language list, cached for 30 days (1 unit when fetched).
func (c *Client) I18nLanguages(ctx context.Context) ([]I18nLanguage, error) {
	if c.store != nil {
		if raw, ok, err := c.store.CacheGet(i18nCacheKey, 30*24*time.Hour); err == nil && ok {
			var cached []I18nLanguage
			if json.Unmarshal([]byte(raw), &cached) == nil {
				return cached, nil
			}
		}
	}
	resp, err := c.svc.I18nLanguages.List([]string{"snippet"}).Hl("en").Context(ctx).Do()
	c.spend("i18nLanguages.list", CostRead)
	if err != nil {
		return nil, apiError(err)
	}
	var out []I18nLanguage
	for _, it := range resp.Items {
		if it.Snippet != nil {
			out = append(out, I18nLanguage{Code: it.Snippet.Hl, Name: it.Snippet.Name})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	if c.store != nil && len(out) > 0 {
		if raw, err := json.Marshal(out); err == nil {
			c.store.CachePut(i18nCacheKey, string(raw))
		}
	}
	return out, nil
}

// APIError is an API failure turned into a sentence a person can act on.
type APIError struct {
	Code   int
	Reason string // the API's reason, e.g. defaultLanguageNotSet
	Msg    string
}

func (e *APIError) Error() string { return e.Msg }

// apiError maps the API's errors to plain sentences.
func apiError(err error) error {
	if errors.Is(err, ErrSignInExpired) {
		return ErrSignInExpired
	}
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return err
	}
	reason := ""
	if len(gerr.Errors) > 0 {
		reason = gerr.Errors[0].Reason
	}
	msg := gerr.Message
	switch reason {
	case "defaultLanguageNotSet":
		msg = "This video has no language set for its title and description. In Studio open the video, then Details > Show more > Title and description language, pick it and save."
	case "quotaExceeded", "dailyLimitExceeded":
		msg = "YouTube's daily limit for this Google project is used up. It resets at midnight Pacific time."
	case "forbidden", "insufficientPermissions":
		msg = "This sign-in cannot change that video. Sign in with the channel that owns it."
	case "videoNotFound":
		msg = "That video was not found, or the signed-in channel cannot see it."
	case "invalidTitle":
		msg = "YouTube refused a title: it must be 100 characters or fewer and cannot contain < or >."
	case "invalidDescription":
		msg = "YouTube refused a description: it must be 5,000 characters or fewer and cannot contain < or >."
	}
	if gerr.Code == http.StatusUnauthorized {
		msg = "The sign-in has expired. Run tongues auth to sign in again."
	}
	if msg == "" {
		msg = fmt.Sprintf("YouTube answered %d", gerr.Code)
	}
	return &APIError{Code: gerr.Code, Reason: reason, Msg: msg}
}
