package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// unitFactors maps a unit to its value in the category's base unit. Temperature
// is handled separately (affine, not a simple factor).
var unitFactors = map[string]map[string]float64{
	"length": { // base: meter
		"mm": 0.001, "cm": 0.01, "m": 1, "km": 1000,
		"in": 0.0254, "ft": 0.3048, "yd": 0.9144, "mi": 1609.344, "nmi": 1852,
	},
	"mass": { // base: gram
		"mg": 0.001, "g": 1, "kg": 1000, "t": 1e6,
		"oz": 28.349523125, "lb": 453.59237,
	},
	"data": { // base: byte
		"b": 1, "kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12,
		"kib": 1024, "mib": 1048576, "gib": 1073741824, "tib": 1099511627776,
	},
	"time": { // base: second
		"ms": 0.001, "s": 1, "min": 60, "h": 3600, "day": 86400, "week": 604800,
	},
}

func unitConvert() mcpserver.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		value := req.GetFloat("value", 0)
		from := strings.ToLower(strings.TrimSpace(req.GetString("from", "")))
		to := strings.ToLower(strings.TrimSpace(req.GetString("to", "")))
		if from == "" || to == "" {
			return mcp.NewToolResultError("both 'from' and 'to' units are required"), nil
		}

		// Temperature is affine — convert via Celsius.
		if isTemp(from) && isTemp(to) {
			c, ok := toCelsius(value, from)
			if !ok {
				return mcp.NewToolResultError("unknown temperature unit " + from), nil
			}
			out, ok := fromCelsius(c, to)
			if !ok {
				return mcp.NewToolResultError("unknown temperature unit " + to), nil
			}
			return mcp.NewToolResultText(fmt.Sprintf("%s %s = %s %s", trimF(value), from, trimF(out), to)), nil
		}

		for cat, units := range unitFactors {
			ff, okF := units[from]
			ft, okT := units[to]
			if okF && okT {
				out := value * ff / ft
				return mcp.NewToolResultText(fmt.Sprintf("%s %s = %s %s (%s)", trimF(value), from, trimF(out), to, cat)), nil
			}
		}
		return mcp.NewToolResultError(fmt.Sprintf("cannot convert %q to %q (units in different categories or unknown)", from, to)), nil
	}
}

func isTemp(u string) bool {
	switch u {
	case "c", "°c", "celsius", "f", "°f", "fahrenheit", "k", "kelvin":
		return true
	}
	return false
}

func toCelsius(v float64, u string) (float64, bool) {
	switch u {
	case "c", "°c", "celsius":
		return v, true
	case "f", "°f", "fahrenheit":
		return (v - 32) * 5 / 9, true
	case "k", "kelvin":
		return v - 273.15, true
	}
	return 0, false
}

func fromCelsius(c float64, u string) (float64, bool) {
	switch u {
	case "c", "°c", "celsius":
		return c, true
	case "f", "°f", "fahrenheit":
		return c*9/5 + 32, true
	case "k", "kelvin":
		return c + 273.15, true
	}
	return 0, false
}

func trimF(f float64) string { return strconv.FormatFloat(f, 'g', 6, 64) }
