package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const maximumFeedItems = 100

type feedPolicyDecision struct {
	PolicyID      string `json:"policyId"`
	PolicyVersion string `json:"policyVersion"`
	SourceHost    string `json:"sourceHost"`
	PathPrefix    string `json:"pathPrefix"`
	Method        string `json:"method"`
	MaximumItems  int    `json:"maximumItems"`
	RetentionDays int    `json:"retentionDays,omitempty"`
}

func (d feedPolicyDecision) Authorize(rawURL string, requestedItems int) error {
	method := strings.ToUpper(strings.TrimSpace(d.Method))
	if method == "" {
		method = http.MethodGet
	}
	host := strings.ToLower(strings.TrimSpace(d.SourceHost))
	prefix := strings.TrimSpace(d.PathPrefix)
	if strings.TrimSpace(d.PolicyID) == "" || strings.TrimSpace(d.PolicyVersion) == "" || host == "" || strings.ContainsAny(host, "/:@[]") ||
		!strings.HasPrefix(prefix, "/") || strings.Contains(prefix, "..") || method != http.MethodGet || d.MaximumItems < 1 || d.MaximumItems > maximumFeedItems ||
		d.RetentionDays < 0 || d.RetentionDays > 3650 {
		return errors.New("source policy decision is invalid")
	}
	target, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || target.Scheme != "https" || target.User != nil || target.Fragment != "" || strings.ToLower(target.Hostname()) != host ||
		(target.Port() != "" && target.Port() != "443") {
		return errors.New("source URL is outside the authorized policy decision")
	}
	if requestedItems < 1 || requestedItems > d.MaximumItems {
		return errors.New("source request exceeds the authorized policy decision")
	}
	path := target.EscapedPath()
	if path == "" {
		path = "/"
	}
	trimmed := strings.TrimSuffix(prefix, "/")
	if path != trimmed && !strings.HasPrefix(path, trimmed+"/") {
		return errors.New("source URL path is outside the authorized policy decision")
	}
	return nil
}

type feedObservation struct {
	StableSourceID string                 `json:"stableSourceId"`
	SourceURI      string                 `json:"sourceUri"`
	ContentDigest  string                 `json:"contentDigest"`
	Summary        string                 `json:"summary"`
	ObservedAt     time.Time              `json:"observedAt"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
}

type feedResult struct {
	Observations []feedObservation `json:"sourceObservations"`
	NextCursor   string            `json:"nextCursor"`
}

type feedDocument struct {
	Title   string      `xml:"title"`
	Channel *rssChannel `xml:"channel"`
	Entries []atomEntry `xml:"entry"`
}

type rssChannel struct {
	Title string    `xml:"title"`
	Items []rssItem `xml:"item"`
}

type rssItem struct {
	GUID        string `xml:"guid"`
	Link        string `xml:"link"`
	Title       string `xml:"title"`
	Description string `xml:"description"`
	Published   string `xml:"pubDate"`
}

type atomEntry struct {
	ID        string     `xml:"id"`
	Title     string     `xml:"title"`
	Summary   string     `xml:"summary"`
	Content   string     `xml:"content"`
	Published string     `xml:"published"`
	Updated   string     `xml:"updated"`
	Links     []atomLink `xml:"link"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
}

var feedTags = regexp.MustCompile(`<[^>]*>`)

func parseFeedSince(data []byte, feedURL string, maxItems int, cursor string) (*feedResult, error) {
	if maxItems < 1 || maxItems > maximumFeedItems {
		return nil, fmt.Errorf("maxItems must be between 1 and %d", maximumFeedItems)
	}
	base, err := url.Parse(feedURL)
	if err != nil || base.Scheme != "https" || base.Hostname() == "" {
		return nil, errors.New("feed URL must be an absolute HTTPS URL")
	}
	var document feedDocument
	if err := xml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse RSS or Atom feed: %w", err)
	}
	feedTitle := cleanFeedText(document.Title)
	cursor = strings.TrimSpace(cursor)
	result := &feedResult{Observations: make([]feedObservation, 0), NextCursor: cursor}
	if document.Channel != nil {
		feedTitle = cleanFeedText(document.Channel.Title)
		for _, item := range document.Channel.Items {
			observation, observationErr := normalizeFeedEntry(base, feedTitle, item.GUID, item.Link, item.Title, item.Description, item.Published)
			if observationErr == nil {
				if cursor != "" && observation.StableSourceID == cursor {
					break
				}
				result.Observations = append(result.Observations, observation)
			}
			if len(result.Observations) == maxItems {
				break
			}
		}
	} else {
		for _, item := range document.Entries {
			link := ""
			for _, candidate := range item.Links {
				if candidate.Rel == "" || candidate.Rel == "alternate" {
					link = candidate.Href
					break
				}
			}
			published := item.Published
			if published == "" {
				published = item.Updated
			}
			summary := item.Summary
			if summary == "" {
				summary = item.Content
			}
			observation, observationErr := normalizeFeedEntry(base, feedTitle, item.ID, link, item.Title, summary, published)
			if observationErr == nil {
				if cursor != "" && observation.StableSourceID == cursor {
					break
				}
				result.Observations = append(result.Observations, observation)
			}
			if len(result.Observations) == maxItems {
				break
			}
		}
	}
	if len(result.Observations) == 0 && cursor == "" {
		return nil, errors.New("feed contained no valid observable entries")
	}
	if len(result.Observations) > 0 {
		result.NextCursor = result.Observations[0].StableSourceID
	}
	return result, nil
}

func normalizeFeedEntry(base *url.URL, feedTitle, stableID, link, title, summary, published string) (feedObservation, error) {
	resolved, err := base.Parse(strings.TrimSpace(link))
	if err != nil || resolved.Scheme != "https" || resolved.Hostname() == "" || resolved.User != nil {
		return feedObservation{}, errors.New("entry link must resolve to an absolute HTTPS URL")
	}
	stableID = strings.TrimSpace(stableID)
	if stableID == "" {
		stableID = resolved.String()
	}
	title, summary = cleanFeedText(title), cleanFeedText(summary)
	if title == "" && summary == "" {
		return feedObservation{}, errors.New("entry title or summary is required")
	}
	combined := title
	if summary != "" && summary != title {
		if combined != "" {
			combined += " — "
		}
		combined += summary
	}
	if len(combined) > 4000 {
		combined = strings.TrimSpace(combined[:4000])
	}
	observedAt := parseFeedTime(published)
	if observedAt.IsZero() {
		return feedObservation{}, errors.New("entry timestamp is required")
	}
	digestInput := stableID + "\x00" + resolved.String() + "\x00" + combined + "\x00" + observedAt.Format(time.RFC3339Nano)
	digest := sha256.Sum256([]byte(digestInput))
	metadata := map[string]interface{}{"format": "rss-atom"}
	if feedTitle != "" {
		metadata["feedTitle"] = feedTitle
	}
	return feedObservation{
		StableSourceID: stableID, SourceURI: resolved.String(), ContentDigest: "sha256:" + hex.EncodeToString(digest[:]),
		Summary: combined, ObservedAt: observedAt.UTC(), Metadata: metadata,
	}, nil
}

func cleanFeedText(value string) string {
	value = html.UnescapeString(feedTags.ReplaceAllString(value, " "))
	return strings.Join(strings.Fields(value), " ")
}

func parseFeedTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
		if parsed, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return parsed
		}
	}
	return time.Time{}
}
