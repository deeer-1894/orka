package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// weather returns current conditions for a location via wttr.in (keyless,
// reliable). Live data lookups belong in a focused tool, not browser automation.
func weather() mcpserver.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		loc := req.GetString("location", "")
		if loc == "" {
			loc = req.GetString("query", "")
		}
		if loc == "" {
			return mcp.NewToolResultError("location is required"), nil
		}
		endpoint := "https://wttr.in/" + url.PathEscape(loc) + "?format=j1"
		hreq, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		hreq.Header.Set("User-Agent", "curl/8")
		hreq.Header.Set("Accept-Language", "zh")
		resp, err := httpSearchC.Do(hreq)
		if err != nil {
			return mcp.NewToolResultError("weather fetch failed: " + err.Error()), nil
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

		var w struct {
			Current []struct {
				TempC      string              `json:"temp_C"`
				FeelsLikeC string              `json:"FeelsLikeC"`
				Humidity   string              `json:"humidity"`
				WindKmph   string              `json:"windspeedKmph"`
				WeatherteDesc []struct{ Value string } `json:"weatherDesc"`
			} `json:"current_condition"`
			Weather []struct {
				Date    string `json:"date"`
				MaxtempC string `json:"maxtempC"`
				MintempC string `json:"mintempC"`
			} `json:"weather"`
		}
		if err := json.Unmarshal(body, &w); err != nil || len(w.Current) == 0 {
			return mcp.NewToolResultError("could not parse weather for " + loc), nil
		}
		c := w.Current[0]
		desc := ""
		if len(c.WeatherteDesc) > 0 {
			desc = c.WeatherteDesc[0].Value
		}
		out := fmt.Sprintf("Weather for %s:\n- now: %s°C (feels %s°C), %s\n- humidity: %s%%, wind: %s km/h\n",
			loc, c.TempC, c.FeelsLikeC, desc, c.Humidity, c.WindKmph)
		if len(w.Weather) > 0 {
			t := w.Weather[0]
			out += fmt.Sprintf("- today (%s): high %s°C / low %s°C\n", t.Date, t.MaxtempC, t.MintempC)
		}
		return mcp.NewToolResultText(out), nil
	}
}
