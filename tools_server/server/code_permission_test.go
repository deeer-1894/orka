package server

import "testing"

func TestCodeToolsShareOptInAndScope(t *testing.T) {
	t.Setenv("SHELL_TOOL", "1")
	for _, enabled := range []string{"0", "1"} {
		t.Run(enabled, func(t *testing.T) {
			t.Setenv("CODE_EXECUTION", enabled)
			endpoint := startGateway(t, Config{Secret: testSecret, BaseStorage: t.TempDir()})
			for _, scoped := range []bool{false, true} {
				var scopes []string
				if scoped {
					scopes = []string{"code:execute"}
				}
				c := connect(t, endpoint, tokenHeader(t, "fixture@example.test", scopes))
				defer c.Close()
				names := listNames(t, c)
				for _, name := range []string{"shell", "python"} {
					if names[name] != (enabled == "1" && scoped) {
						t.Errorf("%s listed=%v enabled=%s scoped=%v", name, names[name], enabled, scoped)
					}
					if enabled == "1" && !scoped {
						res, _ := callText(t, c, name, map[string]any{"command": "printf should-not-run", "code": "print('should-not-run')"})
						if !res.IsError {
							t.Errorf("%s direct call bypassed code scope", name)
						}
					}
				}
			}
		})
	}
}
