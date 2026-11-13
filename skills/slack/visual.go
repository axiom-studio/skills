package main

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// The host sends an answer's visual in the delivery envelope's "visuals"
// field when the adapter declares the visuals feature. These types read
// only what the Block Kit renderer draws and ignore every other field; the
// host validated the surface before sending it, so nothing is re-checked.
type conversationVisual struct {
	ArtifactID string        `json:"artifactId"`
	Version    int64         `json:"version"`
	Surface    visualSurface `json:"surface"`
	// FallbackText is the host's plain-markdown reading of the whole visual.
	FallbackText string `json:"fallbackText"`
}

type visualSurface struct {
	Title      string            `json:"title"`
	Components []visualComponent `json:"components"`
}

// visualComponent is the shared component envelope. Its payload sits under
// the key that equals its type and is decoded only by the renderer that
// draws that type (decodePayload).
type visualComponent struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Summary  string            `json:"summary,omitempty"`
	Title    string            `json:"title,omitempty"`
	Tone     string            `json:"tone,omitempty"`
	Text     string            `json:"text,omitempty"`
	Children []visualComponent `json:"children,omitempty"`
	Tabs     []visualTab       `json:"tabs,omitempty"`
	payload  json.RawMessage
}

type visualTab struct {
	Label    string            `json:"label"`
	Children []visualComponent `json:"children"`
}

func (c *visualComponent) UnmarshalJSON(raw []byte) error {
	type envelope visualComponent
	var decoded envelope
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	*c = visualComponent(decoded)
	switch c.Type {
	case "text", "caption", "divider", "tabs":
	default:
		c.payload = fields[c.Type]
	}
	return nil
}

// decodePayload reads the component's payload into target and reports
// whether it had one of that shape.
func decodePayload(c visualComponent, target interface{}) bool {
	return len(c.payload) > 0 && json.Unmarshal(c.payload, target) == nil
}

// Payload shapes, by component type.

type visualAmount struct {
	Value    float64 `json:"value"`
	Format   string  `json:"format,omitempty"`
	Currency string  `json:"currency,omitempty"`
	Unit     string  `json:"unit,omitempty"`
}

type visualPrice struct {
	visualAmount
	Per string `json:"per,omitempty"`
}

type visualRating struct {
	Value float64 `json:"value"`
	Count *int    `json:"count,omitempty"`
}

type visualLabeled struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Title  string `json:"title"`
	Detail string `json:"detail,omitempty"`
	Group  string `json:"group,omitempty"`
}

type visualCallout struct {
	Body string `json:"body"`
}

type visualStatRow struct {
	Stats []struct {
		Label string `json:"label"`
		visualAmount
		Delta string `json:"delta,omitempty"`
		Trend string `json:"trend,omitempty"`
	} `json:"stats"`
}

type visualTable struct {
	Columns []struct {
		Key      string `json:"key"`
		Label    string `json:"label"`
		Kind     string `json:"kind"`
		Currency string `json:"currency,omitempty"`
	} `json:"columns"`
	Rows []map[string]json.RawMessage `json:"rows"`
}

type visualSteps struct {
	Items     []visualLabeled `json:"items"`
	Checkable bool            `json:"checkable,omitempty"`
}

type visualTimeline struct {
	Events []struct {
		When   string `json:"when"`
		Title  string `json:"title"`
		Detail string `json:"detail,omitempty"`
	} `json:"events"`
}

type visualItem struct {
	Name   string        `json:"name"`
	Badge  string        `json:"badge,omitempty"`
	Price  *visualPrice  `json:"price,omitempty"`
	Rating *visualRating `json:"rating,omitempty"`
	Facts  []string      `json:"facts,omitempty"`
}

type visualPlace struct {
	Name     string        `json:"name,omitempty"`
	Category string        `json:"category,omitempty"`
	Address  string        `json:"address,omitempty"`
	Rating   *visualRating `json:"rating,omitempty"`
	Hours    []string      `json:"hours,omitempty"`
	Facts    []string      `json:"facts,omitempty"`
}

type visualDefinition struct {
	Term         string `json:"term"`
	Phonetic     string `json:"phonetic,omitempty"`
	PartOfSpeech string `json:"partOfSpeech,omitempty"`
	Meaning      string `json:"meaning"`
	Example      string `json:"example,omitempty"`
}

type visualAgenda struct {
	Days []struct {
		Date  string `json:"date"`
		Label string `json:"label,omitempty"`
		Items []struct {
			Start string `json:"start"`
			End   string `json:"end,omitempty"`
			Title string `json:"title"`
			Place string `json:"place,omitempty"`
		} `json:"items"`
	} `json:"days"`
}

type visualItinerary struct {
	Days []struct {
		Label string `json:"label"`
		Items []struct {
			Time  string `json:"time,omitempty"`
			Title string `json:"title"`
			Place string `json:"place,omitempty"`
		} `json:"items"`
	} `json:"days"`
}

type visualRecipe struct {
	Servings    int `json:"servings"`
	Ingredients []struct {
		Qty  *float64 `json:"qty,omitempty"`
		Unit string   `json:"unit,omitempty"`
		Name string   `json:"name"`
	} `json:"ingredients"`
	Steps []struct {
		Text string `json:"text"`
	} `json:"steps"`
}

type visualProgress struct {
	Items []struct {
		Label    string  `json:"label"`
		Value    float64 `json:"value"`
		Target   float64 `json:"target"`
		Format   string  `json:"format,omitempty"`
		Currency string  `json:"currency,omitempty"`
		Unit     string  `json:"unit,omitempty"`
	} `json:"items"`
}

type visualTasks struct {
	Rows []struct {
		Label string `json:"label"`
	} `json:"rows"`
}

type visualRoutine struct {
	Items []struct {
		Label string `json:"label"`
		Text  string `json:"text"`
	} `json:"items,omitempty"`
}

type visualAgent struct {
	Headline string   `json:"headline"`
	Points   []string `json:"points,omitempty"`
}

type visualImages struct {
	// Caption belongs to image; Images to gallery; Markers to labeled_image.
	Caption string `json:"caption,omitempty"`
	Images  []struct {
		Caption string `json:"caption,omitempty"`
	} `json:"images,omitempty"`
	Markers []visualLabeled `json:"markers,omitempty"`
}

// visualOptions reads the options of choices (chips), quiz and poll
// (question, options) and actions (the payload is the list itself).
type visualOptions struct {
	Question string          `json:"question,omitempty"`
	Chips    []visualLabeled `json:"chips,omitempty"`
	Options  []visualLabeled `json:"options,omitempty"`
}

type visualForm struct {
	Description string `json:"description,omitempty"`
	Fields      []struct {
		Label    string `json:"label"`
		Required bool   `json:"required,omitempty"`
		Options  []struct {
			Label string `json:"label"`
		} `json:"options,omitempty"`
	} `json:"fields"`
}

type visualFlashcards struct {
	Cards []struct {
		Front string `json:"front"`
	} `json:"cards"`
}

type visualExplorer struct {
	Inputs []struct {
		Label   string   `json:"label"`
		Default *float64 `json:"default"`
		Unit    string   `json:"unit,omitempty"`
	} `json:"inputs"`
	Outputs []struct {
		Label string `json:"label"`
	} `json:"outputs"`
}

type visualDraft struct {
	Channel string `json:"channel"`
	To      string `json:"to,omitempty"`
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body"`
}

type visualChart struct {
	visualAmount
	Series []struct {
		Name   string `json:"name"`
		Points []struct {
			Label string  `json:"label"`
			Value float64 `json:"value"`
		} `json:"points"`
	} `json:"series"`
}

type visualBreakdown struct {
	visualAmount
	Items []struct {
		Label string  `json:"label"`
		Value float64 `json:"value"`
	} `json:"items"`
}

type visualDiagram struct {
	Nodes []visualLabeled `json:"nodes"`
	Edges []struct {
		From  string `json:"from"`
		To    string `json:"to"`
		Label string `json:"label,omitempty"`
	} `json:"edges,omitempty"`
}

type visualMap struct {
	Pins []struct {
		Place string `json:"place"`
		Label string `json:"label,omitempty"`
	} `json:"pins"`
}

type visualMath struct {
	Steps []struct {
		LaTeX string `json:"latex"`
		Note  string `json:"note,omitempty"`
	} `json:"steps"`
}

type visualComparison struct {
	Attributes []struct {
		Key   string `json:"key"`
		Label string `json:"label"`
	} `json:"attributes"`
	Options []struct {
		ID     string            `json:"id"`
		Name   string            `json:"name"`
		Badge  string            `json:"badge,omitempty"`
		Price  *visualPrice      `json:"price,omitempty"`
		Values map[string]string `json:"values"`
	} `json:"options"`
	Recommended string `json:"recommended,omitempty"`
}

type visualBeforeAfter struct {
	Before struct {
		Text string `json:"text,omitempty"`
	} `json:"before"`
	After struct {
		Text string `json:"text,omitempty"`
	} `json:"after"`
	Labels *struct {
		Before string `json:"before,omitempty"`
		After  string `json:"after,omitempty"`
	} `json:"labels,omitempty"`
}

type visualAnalogy struct {
	From struct {
		Label string `json:"label"`
	} `json:"from"`
	To struct {
		Label string `json:"label"`
	} `json:"to"`
	Mapping []struct {
		A string `json:"a"`
		B string `json:"b"`
	} `json:"mapping"`
}

// amountText writes a number in its stat format: "GBP 96,000", "12%",
// "1h 5m", "28 km".
func amountText(amount visualAmount) string {
	var text string
	switch amount.Format {
	case "currency":
		text = strings.TrimSpace(amount.Currency + " " + groupedNumber(amount.Value))
	case "percent":
		text = groupedNumber(amount.Value) + "%"
	case "duration":
		text = durationText(amount.Value)
	default:
		text = groupedNumber(amount.Value)
	}
	if unit := strings.TrimSpace(amount.Unit); unit != "" {
		text += " " + unit
	}
	return text
}

func groupedNumber(value float64) string {
	text := strconv.FormatFloat(value, 'f', -1, 64)
	if strings.ContainsAny(text, "eE") {
		return text
	}
	sign := ""
	if strings.HasPrefix(text, "-") {
		sign, text = "-", text[1:]
	}
	whole, fraction, _ := strings.Cut(text, ".")
	var grouped strings.Builder
	for index, digit := range whole {
		if index > 0 && (len(whole)-index)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(digit)
	}
	if fraction != "" {
		return sign + grouped.String() + "." + fraction
	}
	return sign + grouped.String()
}

func durationText(seconds float64) string {
	total := int64(math.Round(seconds))
	hours, minutes, rest := total/3600, total%3600/60, total%60
	var parts []string
	if hours > 0 {
		parts = append(parts, strconv.FormatInt(hours, 10)+"h")
	}
	if minutes > 0 {
		parts = append(parts, strconv.FormatInt(minutes, 10)+"m")
	}
	if rest > 0 || len(parts) == 0 {
		parts = append(parts, strconv.FormatInt(rest, 10)+"s")
	}
	return strings.Join(parts, " ")
}

func priceText(price *visualPrice) string {
	if price == nil {
		return ""
	}
	text := amountText(price.visualAmount)
	if price.Per != "" && price.Per != "once" {
		text += "/" + price.Per
	}
	return text
}

func ratingText(rating *visualRating) string {
	if rating == nil {
		return ""
	}
	return "★ " + strconv.FormatFloat(rating.Value, 'f', -1, 64)
}
