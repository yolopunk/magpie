package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yetone/magpie/internal/catalog"
	"github.com/yetone/magpie/internal/provider"
	"github.com/yetone/magpie/internal/testenv"
)

// nanobotConfig is a config.json as nanobot's docs have a user write one
// (docs/providers.md's custom provider, camelCase), with the preset naming
// it and a gateway of the user's own.
const nanobotConfig = `{
  "providers": {
    "custom": {
      "apiKey": "${CUSTOM_API_KEY}",
      "apiBase": "https://api.your-provider.com/v1"
    }
  },
  "modelPresets": {
    "primary": {
      "provider": "custom",
      "model": "provider-model-name",
      "maxTokens": 8192,
      "contextWindowTokens": 65536
    }
  },
  "agents": {
    "defaults": {
      "modelPreset": "primary"
    }
  },
  "gateway": {
    "host": "127.0.0.1",
    "port": 8080
  }
}
`

// nanobotHome is a HOME with magpie serving a provider of two models, one of
// them with a window and effort levels, and nanobot's ~/.nanobot/config.json
// holding body when body isn't empty. PATH is emptied, so only nanobot's
// files say it is here.
func nanobotHome(t *testing.T, body string) (home, path string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("PATH", t.TempDir())
	if err := provider.Save(provider.Provider{ID: "kimi", Name: "Kimi", Key: "k",
		Chat: "https://api.moonshot.cn/v1", Models: []string{"k2", "k2-vision"}}); err != nil {
		t.Fatal(err)
	}
	if err := catalog.SaveLive("kimi", "https://api.moonshot.cn/v1", []catalog.Model{
		{ID: "k2", Context: 262144, Output: 8192, Efforts: []string{"low", "medium", "high"}},
		{ID: "k2-vision", Images: true},
	}); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(home, ".nanobot", "config.json")
	if body != "" {
		writeFile(t, path, body)
	}
	return home, path
}

func nanobotRead(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", path, err, b)
	}
	return m
}

func nanobotTable(m map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		m, _ = m[k].(map[string]any)
	}
	return m
}

// Connecting nanobot writes its three keys: the provider "magpie" at the
// gateway's /v1 with a key naming nanobot, the preset "magpie" naming it with
// the model picked and its window and reply limit, and the preset a session
// starts on. The user's own provider, preset and settings stay, a preset of
// their own picked in magpie takes magpie out and puts theirs back, and
// Disconnect gives the file back byte for byte.
func TestNanobot(t *testing.T) {
	home, path := nanobotHome(t, nanobotConfig)
	a := nanobot(home)
	if a.Path != path || a.Dir != filepath.Join(home, ".nanobot") || a.Bin != "nanobot" {
		t.Fatalf("path %q dir %q bin %q", a.Path, a.Dir, a.Bin)
	}
	f := a.Field("model")
	if got := f.Get(); got != "primary" || a.Wired() || a.Check() != "" {
		t.Fatalf("before: %q wired %v check %q", got, a.Wired(), a.Check())
	}
	var own, ours bool
	for _, o := range f.Options(a.Values()) {
		own = own || o.Value == "primary"
		ours = ours || o.Value == "magpie/kimi/k2"
	}
	if !own || !ours {
		t.Fatalf("options: own %v magpie %v", own, ours)
	}

	if err := a.Pick("model", "magpie/kimi/k2"); err != nil {
		t.Fatal(err)
	}
	gw := here(home)
	m := nanobotRead(t, path)
	if p := nanobotTable(m, "providers", "magpie"); p["apiBase"] != gw.v1() || p["apiKey"] != agentKeyAt("nanobot", gw.gw()) {
		t.Fatalf("magpie's provider: %v", p)
	}
	if p := nanobotTable(m, "modelPresets", "magpie"); p["provider"] != "magpie" || p["model"] != "kimi/k2" ||
		p["contextWindowTokens"] != float64(262144) || p["maxTokens"] != float64(8192) {
		t.Fatalf("magpie's preset: %v", p)
	}
	if nanobotTable(m, "agents", "defaults")["modelPreset"] != "magpie" {
		t.Fatalf("defaults: %v", m["agents"])
	}
	if !a.Wired() || f.Get() != "magpie/kimi/k2" {
		t.Fatalf("wired %v, model %q", a.Wired(), f.Get())
	}
	if c := a.Check(); c != "" {
		t.Fatalf("check: %s", c)
	}
	// the user's own blocks and settings, as written
	if p := nanobotTable(m, "providers", "custom"); p["apiBase"] != "https://api.your-provider.com/v1" || p["apiKey"] != "${CUSTOM_API_KEY}" {
		t.Fatalf("the user's provider: %v", p)
	}
	if p := nanobotTable(m, "modelPresets", "primary"); p["model"] != "provider-model-name" || p["contextWindowTokens"] != float64(65536) {
		t.Fatalf("the user's preset: %v", p)
	}
	if nanobotTable(m, "gateway")["port"] != float64(8080) {
		t.Fatalf("the user's gateway: %v", m["gateway"])
	}

	// a preset of the user's own picked in magpie takes magpie out
	if err := a.Pick("model", "primary"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != nanobotConfig {
		t.Fatalf("after their own pick:\n%s", got)
	}
	if a.Wired() || f.Get() != "primary" {
		t.Fatalf("wired %v, model %q", a.Wired(), f.Get())
	}

	// and back on magpie, Disconnect puts the file back as it was
	if err := a.Pick("model", "magpie/kimi/k2-vision"); err != nil {
		t.Fatal(err)
	}
	if p := nanobotTable(nanobotRead(t, path), "modelPresets", "magpie"); p["model"] != "kimi/k2-vision" || p["contextWindowTokens"] != nil {
		// k2-vision's window is not known: nanobot's own default serves
		t.Fatalf("k2-vision's preset: %v", p)
	}
	if err := a.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != nanobotConfig {
		t.Fatalf("Disconnect left:\n%s", got)
	}
	if a.Wired() {
		t.Fatal("still wired")
	}
}

// Off — the field's empty value — takes magpie's wiring out: nanobot back on
// the preset it started on before, the blocks gone and the maps they made
// gone with them. A config.json magpie made goes entirely.
func TestNanobotSetEmptyCleansUp(t *testing.T) {
	home, path := nanobotHome(t, nanobotConfig)
	a := nanobot(home)
	if err := a.Pick("model", "magpie/kimi/k2"); err != nil {
		t.Fatal(err)
	}
	if err := a.Field("model").Set(""); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != nanobotConfig {
		t.Fatalf("off left:\n%s", got)
	}
	if a.Wired() || a.Field("model").Get() != "primary" {
		t.Fatalf("wired %v, model %q", a.Wired(), a.Field("model").Get())
	}

	// no config of the user's: magpie made the file, and off takes it away
	home, path = nanobotHome(t, "")
	a = nanobot(home)
	if a.Detected() {
		t.Fatal("found with nothing of its own")
	}
	if err := a.Pick("model", "magpie/kimi/k2"); err != nil {
		t.Fatal(err)
	}
	if !a.Detected() {
		t.Fatal("not found with its config")
	}
	if err := a.Field("model").Set(""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("magpie's own file left behind: %v", err)
	}
}

// What the user puts in magpie's own blocks stays through a sync: a proxy and
// headers on the provider, a temperature on the preset. The endpoint follows
// the gateway and the preset's window the catalog, and a sync with nothing to
// change writes nothing.
func TestNanobotKeepsTheUsersSettings(t *testing.T) {
	home, path := nanobotHome(t, nanobotConfig)
	a := nanobot(home)
	if err := a.Pick("model", "magpie/kimi/k2"); err != nil {
		t.Fatal(err)
	}
	gw := here(home)
	// as nanobot's own WebUI saves it, sorted and compact, with what the user
	// changed in magpie's blocks
	saved := `{"agents":{"defaults":{"modelPreset":"magpie"}},` +
		`"gateway":{"host":"127.0.0.1","port":8080},` +
		`"modelPresets":{"magpie":{"contextWindowTokens":262144,"maxTokens":8192,` +
		`"model":"kimi/k2","provider":"magpie","temperature":0.2},` +
		`"primary":{"contextWindowTokens":65536,"maxTokens":8192,"model":"provider-model-name","provider":"custom"}},` +
		`"providers":{"custom":{"apiBase":"https://api.your-provider.com/v1","apiKey":"${CUSTOM_API_KEY}"},` +
		`"magpie":{"apiBase":"` + gw.v1() + `","apiKey":"` + agentKeyAt("nanobot", gw.gw()) + `",` +
		`"extraHeaders":{"X-Team":"mine"},"proxy":"http://127.0.0.1:7890"}}}`
	writeFile(t, path, saved)
	// the model's window in the catalog changed
	if err := catalog.SaveLive("kimi", "https://api.moonshot.cn/v1", []catalog.Model{
		{ID: "k2", Context: 131072, Output: 8192, Efforts: []string{"low", "medium", "high"}},
		{ID: "k2-vision", Images: true},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	m := nanobotRead(t, path)
	if p := nanobotTable(m, "modelPresets", "magpie"); p["contextWindowTokens"] != float64(131072) || p["temperature"] != 0.2 {
		t.Fatalf("the preset: %v", p)
	}
	if p := nanobotTable(m, "providers", "magpie"); p["proxy"] != "http://127.0.0.1:7890" ||
		nanobotTable(m, "providers", "magpie", "extraHeaders")["X-Team"] != "mine" {
		t.Fatalf("the provider: %v", p)
	}
	if c := a.Check(); c != "" {
		t.Fatalf("check: %s", c)
	}

	// nothing to change: the file isn't written
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	if err := a.Sync(); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); !st.ModTime().Equal(old) {
		t.Fatalf("a sync with nothing to change wrote the file (%v)", st.ModTime())
	}
}

// Check says what of magpie's wiring is gone while nanobot still starts on
// it: the provider moved elsewhere or gone, the preset gone; nothing said
// when nanobot starts on a preset of the user's own.
func TestNanobotCheck(t *testing.T) {
	home, path := nanobotHome(t, nanobotConfig)
	a := nanobot(home)
	if err := a.Pick("model", "magpie/kimi/k2"); err != nil {
		t.Fatal(err)
	}
	wire := func(edit string) {
		t.Helper()
		m := nanobotRead(t, path)
		switch edit {
		case "apiBase":
			nanobotTable(m, "providers", "magpie")["apiBase"] = "https://elsewhere.example/v1"
		case "no provider":
			delete(nanobotTable(m, "providers"), "magpie")
		case "no preset":
			delete(nanobotTable(m, "modelPresets"), "magpie")
		}
		b, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, path, string(b))
	}
	wire("apiBase")
	if c := a.Check(); !strings.Contains(c, "apiBase") || !strings.Contains(c, "was changed") {
		t.Fatalf("check: %q", c)
	}
	wire("no provider")
	if c := a.Check(); !strings.Contains(c, "providers.magpie") || !strings.Contains(c, "gone") {
		t.Fatalf("check: %q", c)
	}
	// wired again, the preset gone alone
	if err := a.Pick("model", "magpie/kimi/k2"); err != nil {
		t.Fatal(err)
	}
	wire("no preset")
	if c := a.Check(); !strings.Contains(c, "modelPresets.magpie") {
		t.Fatalf("check: %q", c)
	}
	// a preset of the user's own: not magpie's, nothing said
	if err := a.Field("model").Set("primary"); err != nil {
		t.Fatal(err)
	}
	if c := a.Check(); c != "" {
		t.Fatalf("check: %q", c)
	}
}

// A preset of the user's own whose name holds a dot is one preset, not two
// steps of a key path: the picker lists it by name and picking it writes that
// name, which is how nanobot's own config names it. A dotted name that is no
// preset is refused like any other.
func TestNanobotDottedPresetName(t *testing.T) {
	home, path := nanobotHome(t, strings.ReplaceAll(nanobotConfig, `"primary"`, `"my.preset"`))
	a := nanobot(home)
	f := a.Field("model")
	if got := f.Get(); got != "my.preset" {
		t.Fatalf("get: %q", got)
	}
	own := false
	for _, o := range f.Options(a.Values()) {
		own = own || o.Value == "my.preset"
	}
	if !own {
		t.Fatal("the picker doesn't list the dotted preset")
	}
	if err := a.Pick("model", "magpie/kimi/k2"); err != nil {
		t.Fatal(err)
	}
	if err := f.Set("my.preset"); err != nil {
		t.Fatalf("a dotted preset of the user's own: %v", err)
	}
	m := nanobotRead(t, path)
	if nanobotTable(m, "agents", "defaults")["modelPreset"] != "my.preset" {
		t.Fatalf("defaults: %v", m["agents"])
	}
	if p := nanobotTable(m, "modelPresets", "my.preset"); p["model"] != "provider-model-name" {
		t.Fatalf("the user's preset: %v", m["modelPresets"])
	}
	if a.Wired() {
		t.Fatal("still wired")
	}
	if err := f.Set("no.such.preset"); err == nil || !strings.Contains(err.Error(), "no model preset") {
		t.Fatalf("a dotted name that is no preset: %v", err)
	}
}

// A config.json nanobot can't read is left as it is: a model picked writes
// nothing over it. A value that names no preset of the user's own is refused
// rather than written as the one a session starts on.
func TestNanobotLeavesWhatItCannotRead(t *testing.T) {
	home, path := nanobotHome(t, "")
	a := nanobot(home)
	writeFile(t, path, "{not json")
	if err := a.Pick("model", "magpie/kimi/k2"); err == nil {
		t.Fatal("picked into a config that isn't JSON")
	}
	if got, _ := os.ReadFile(path); string(got) != "{not json" {
		t.Fatalf("written over: %s", got)
	}

	writeFile(t, path, nanobotConfig)
	if err := a.Field("model").Set("no-such-preset"); err == nil || !strings.Contains(err.Error(), "no model preset") {
		t.Fatalf("a value naming no preset: %v", err)
	}
	if nanobotTable(nanobotRead(t, path), "agents", "defaults")["modelPreset"] != "primary" {
		t.Fatal("the refused pick changed the file")
	}
}

// nanobot is found by its config.json, its folder or its command, and by
// nothing else on a machine without it.
func TestNanobotDetected(t *testing.T) {
	home, path := nanobotHome(t, "")
	a := nanobot(home)
	if a.Detected() {
		t.Fatal("found with nothing of its own")
	}
	os.MkdirAll(filepath.Dir(path), 0o755) // ~/.nanobot, empty: nanobot's own
	if !a.Detected() {
		t.Fatal("not found with its folder")
	}
	os.Remove(filepath.Dir(path))
	writeFile(t, path, "{}\n") // a config.json alone
	if !a.Detected() {
		t.Fatal("not found with its config")
	}
	os.Remove(path)

	// its command on PATH
	bin := t.TempDir()
	testenv.Program(t, filepath.Join(bin, "nanobot"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", bin)
	if !a.Detected() {
		t.Fatal("not found with its command")
	}
}
