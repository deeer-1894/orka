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

		type desc struct{ Value string }
		var w struct {
			Current []struct {
				TempC       string `json:"temp_C"`
				FeelsLikeC  string `json:"FeelsLikeC"`
				Humidity    string `json:"humidity"`
				WindKmph    string `json:"windspeedKmph"`
				WeatherDesc []desc `json:"weatherDesc"`
			} `json:"current_condition"`
			Weather []struct {
				Date     string `json:"date"`
				MaxtempC string `json:"maxtempC"`
				MintempC string `json:"mintempC"`
				Hourly   []struct {
					Time        string `json:"time"`
					WeatherDesc []desc `json:"weatherDesc"`
					ChanceRain  string `json:"chanceofrain"`
				} `json:"hourly"`
			} `json:"weather"`
		}
		if err := json.Unmarshal(body, &w); err != nil || len(w.Current) == 0 {
			return mcp.NewToolResultError("could not parse weather for " + loc), nil
		}
		c := w.Current[0]
		cur := ""
		if len(c.WeatherDesc) > 0 {
			cur = c.WeatherDesc[0].Value
		}
		out := fmt.Sprintf("Weather for %s:\n- now: %s°C (feels %s°C), %s, humidity %s%%, wind %s km/h\n",
			loc, c.TempC, c.FeelsLikeC, cur, c.Humidity, c.WindKmph)
		if len(w.Weather) > 0 {
			out += "Forecast (free source provides ~3 days):\n"
			for _, d := range w.Weather {
				dd := "" // midday description
				rain := ""
				if len(d.Hourly) >= 5 {
					if len(d.Hourly[4].WeatherDesc) > 0 {
						dd = d.Hourly[4].WeatherDesc[0].Value
					}
					rain = d.Hourly[4].ChanceRain
				}
				out += fmt.Sprintf("- %s: %s–%s°C, %s (rain %s%%)\n", d.Date, d.MintempC, d.MaxtempC, dd, rain)
			}
		}
		return mcp.NewToolResultText(out), nil
	}
}
