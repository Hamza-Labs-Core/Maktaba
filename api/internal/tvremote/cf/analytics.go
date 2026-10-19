package cf

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// streamMinutesQuery reads minutes delivered per day (design §9 cost
// estimate); limit 1 for the permission probe.
const streamMinutesQuery = `query StreamMinutes($account: string!, $since: Date!, $limit: Int!) {
  viewer {
    accounts(filter: {accountTag: $account}) {
      streamMinutesViewedAdaptiveGroups(limit: $limit, filter: {date_geq: $since}, orderBy: [date_ASC]) {
        sum { minutesViewed }
        dimensions { date }
      }
    }
  }
}`

// DayMinutes is one day of minutes delivered.
type DayMinutes struct {
	Date    string  `json:"date"`
	Minutes float64 `json:"minutes"`
}

// MinutesViewed returns the minutes delivered per day since since (GraphQL
// Analytics; needs Account Analytics Read).
func (c *Client) MinutesViewed(ctx context.Context, since time.Time, limit int) ([]DayMinutes, error) {
	if limit <= 0 {
		limit = 31
	}
	body := map[string]any{
		"query": streamMinutesQuery,
		"variables": map[string]any{
			"account": c.AccountID, "since": since.UTC().Format("2006-01-02"), "limit": limit,
		},
	}
	raw, err := c.doRaw(ctx, http.MethodPost, "/graphql", body)
	if err != nil {
		return nil, err
	}
	var out struct {
		Data struct {
			Viewer struct {
				Accounts []struct {
					Groups []struct {
						Sum struct {
							MinutesViewed float64 `json:"minutesViewed"`
						} `json:"sum"`
						Dimensions struct {
							Date string `json:"date"`
						} `json:"dimensions"`
					} `json:"streamMinutesViewedAdaptiveGroups"`
				} `json:"accounts"`
			} `json:"viewer"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("cloudflare graphql: %w", err)
	}
	if len(out.Errors) > 0 {
		return nil, &APIError{Status: http.StatusOK, Method: http.MethodPost, Path: "/graphql",
			Errors: []Error{{Message: out.Errors[0].Message}}}
	}
	var days []DayMinutes
	for _, a := range out.Data.Viewer.Accounts {
		for _, g := range a.Groups {
			days = append(days, DayMinutes{Date: g.Dimensions.Date, Minutes: g.Sum.MinutesViewed})
		}
	}
	return days, nil
}
