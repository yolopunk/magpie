package agent

// Nanobot (HKUDS/nanobot, the nanobot command; PyPI nanobot-ai) is a personal
// AI agent framework that runs in a browser, a terminal or a chat channel,
// with a gateway of its own beside. It keeps its settings in one
// ~/.nanobot/config.json (nanobot/config/home.py; no environment variable
// moves the folder), JSON with camelCase keys, snake_case accepted too
// (nanobot/config/schema.py). A provider of the user's own is a named key
// under providers — any name but a built-in's — with apiBase (a custom
// provider has no default without it) and apiKey; an OpenAI-compatible
// endpoint needs no api block of its own (docs/providers.md):
//
//	{"providers":{"custom":{"apiKey":"${CUSTOM_API_KEY}",
//	   "apiBase":"https://api.your-provider.com/v1"}},
//	 "modelPresets":{"primary":{"provider":"custom",
//	   "model":"provider-model-name","maxTokens":8192,
//	   "contextWindowTokens":65536}},
//	 "agents":{"defaults":{"modelPreset":"primary"}}}
//
// Which model a session starts on is a named model preset,
// modelPresets.<name>: provider (that providers key) and model, with
// maxTokens (default 8192) and contextWindowTokens (default 200000) beside.
// agents.defaults.modelPreset names the preset a session starts on;
// agents.defaults.model with a provider is the older spelling, which
// modelPreset outranks, so magpie writes the preset pointer alone and leaves
// the older keys as they are.
//
// magpie adds the provider "magpie" at the gateway's /v1 and the preset
// "magpie" naming it, and starts nanobot on the model picked, its window and
// reply limit told beside it. The provider's key names nanobot: on the
// OpenAI-compatible backend its requests carry the OpenAI Python SDK's
// User-Agent, nothing of nanobot's own (openai_compat_provider.py), so its
// key is what tells them apart. What agents.defaults.modelPreset named before
// is stashed and put back when magpie steps out; a preset the user picks in
// nanobot since is theirs and stands. Everything else in the file — the
// user's own providers and presets, their gateway and channels — stays as
// written, and so do the keys they add inside magpie's own blocks (a proxy,
// extraHeaders on the provider, a preset's temperature), which is what a
// gateway reached from elsewhere may need (theirsKept).

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/yetone/magpie/internal/edit"
)

// nanobot's three keys in config.json: the provider block, the model preset
// naming it, and the setting that starts a session on a preset.
const (
	nanobotProvider = "providers." + magpieID
	nanobotPreset   = "modelPresets." + magpieID
	nanobotDefault  = "agents.defaults.modelPreset"
)

// what nanobot says after a change (notice.go)
var noticeNanobot = newNotice("Nanobot reads its config when it starts — restart running nanobot (its gateway and webui) to use this.")

func nanobot(home string) *Agent { return nanobotAt(here(home)) }

func nanobotAt(at place) *Agent {
	dir := filepath.Join(at.home, ".nanobot")
	path := filepath.Join(dir, "config.json")
	key := func() string { return agentKeyAt("nanobot", at.gw()) }
	// what agents.defaults.modelPreset named before magpie, "" for none
	keyPick := at.key("nanobot.pick")
	// a config.json magpie made, which goes again once nothing is left in it
	keyNew := at.key("nanobot:" + path + ":new")
	get := func(k string) string { v, _ := edit.GetJSON(path, k); return v }
	ours := func() bool { return get(nanobotDefault) == magpieID }
	wired := func() bool { _, ok := edit.GetJSON(path, nanobotPreset); return ok }
	// provider is magpie's provider block, what the user added in it kept
	provider := theirsKept(path, nanobotProvider, func() any {
		return map[string]any{"apiBase": at.v1(), "apiKey": key()}
	})
	// preset is magpie's model preset for model over what is there: the
	// provider, the model and its window and reply limit are magpie's, set
	// in place or gone, and every other key in the block (a temperature) the
	// user's, kept (nanobotPresetKept)
	preset := func(model string) func() any {
		return func() any { return nanobotPresetKept(path, model) }
	}
	// put is magpie's whole wiring in one write: the provider, the preset and
	// the preset a session starts on
	put := func(model string) error {
		if get(nanobotDefault) != magpieID {
			stash(map[string]string{keyPick: get(nanobotDefault)})
		}
		if !isFile(path) {
			stash(map[string]string{keyNew: "1"})
		}
		return edit.SetJSON(path,
			edit.KV{Path: nanobotProvider, Value: provider()},
			edit.KV{Path: nanobotPreset, Value: preset(model)()},
			edit.KV{Path: nanobotDefault, Value: magpieID},
		)
	}
	// unwire takes magpie's wiring out and puts nanobot back on the preset it
	// started on before — when it still starts on magpie's; a preset picked
	// in nanobot since is the user's and stands (own names the one to go to)
	unwire := func(own string) error {
		var err error
		if ours() {
			was := unstash(keyPick)
			if own != "" {
				was = own
			}
			if was == "" {
				err = edit.DelJSON(path, nanobotDefault)
			} else {
				err = edit.SetJSON(path, edit.KV{Path: nanobotDefault, Value: was})
			}
		} else {
			forget(keyPick)
			if own != "" {
				err = edit.SetJSON(path, edit.KV{Path: nanobotDefault, Value: own})
			}
		}
		if err != nil {
			return err
		}
		if err := edit.DelJSON(path, nanobotPreset, nanobotProvider); err != nil {
			return err
		}
		// a map left holding nothing goes: nanobot omits it, and magpie's own
		// file goes with its last key. agents.defaults before agents, so a
		// defaults left empty empties agents too
		for _, k := range []string{"modelPresets", "providers", "agents.defaults", "agents"} {
			if v, ok := edit.GetJSON(path, k); ok && strings.TrimSpace(v) == "{}" {
				if err := edit.DelJSON(path, k); err != nil {
					return err
				}
			}
		}
		if unstash(keyNew) != "" {
			if raw, err := edit.Read(path); err == nil && len(gjson.ParseBytes(raw).Map()) == 0 {
				return edit.Remove(path)
			}
		}
		return nil
	}
	return atomic(&Agent{
		ID: "nanobot", Name: "Nanobot", Icon: "generic", Spelled: prefixed, NoNames: true,
		Bin: "nanobot", Dir: dir, Path: path,
		Notice: func() string {
			if Running(`(^|/)nanobot( |$)`) {
				return noticeNanobot.String()
			}
			return ""
		},
		Check: func() string {
			if !ours() {
				return ""
			}
			if _, ok := edit.GetJSON(path, nanobotProvider); !ok {
				return "Nanobot's providers.magpie (config.json) is gone, so it no longer reaches magpie"
			}
			if _, ok := edit.GetJSON(path, nanobotPreset); !ok {
				return "Nanobot's modelPresets.magpie (config.json) is gone, so it no longer starts on magpie"
			}
			return wiringOff("Nanobot", path, func(k string) (string, bool) {
				return edit.GetJSON(path, nanobotProvider+"."+k)
			}, "apiBase", at.v1(), "apiKey", key())
		},
		Sync: func() error {
			if !wired() {
				return nil
			}
			// the endpoint follows the gateway, and the preset's window and
			// reply limit the model it names, when that is one of magpie's
			if err := syncJSON(path, nanobotProvider, provider); err != nil {
				return err
			}
			return syncJSON(path, nanobotPreset, preset(get(nanobotPreset+".model")))
		},
		Fields: []Field{{
			// the model preset a nanobot session starts on: one of magpie's
			// is magpie/<provider>/<model>, magpie's preset with the model's
			// id in it; one of the user's own is its name
			Key: "model", Label: "model",
			Get: func() string {
				if !ours() {
					return get(nanobotDefault)
				}
				if m := get(nanobotPreset + ".model"); m != "" {
					return magpieID + "/" + m
				}
				return magpieID
			},
			Set: func(v string) error {
				if ref, ok := strings.CutPrefix(v, magpieID+"/"); ok && isMagpie(ref) {
					return put(ref)
				}
				// a preset of the user's own by name; anything else would
				// name no preset nanobot has (#1457)
				if v != "" {
					if _, ok := nanobotPresets(path)[v]; !ok {
						return fmt.Errorf("%s is no model preset in nanobot's config.json", v)
					}
				}
				if wired() || ours() {
					return unwire(v)
				}
				if v == "" {
					return nil
				}
				return edit.SetJSON(path, edit.KV{Path: nanobotDefault, Value: v})
			},
			Options: func(cur map[string]string) []Option {
				return append(nanobotOwn(path, cur["model"]), viaMagpie("nanobot", magpieID+"/")...)
			},
		}},
	}, path)
}

// nanobotOurs are the keys of magpie's model preset that are magpie's alone,
// set in place or taken out; any other key in the block is the user's.
var nanobotOurs = []string{"provider", "model", "contextWindowTokens", "maxTokens"}

// nanobotPresetKept is magpie's model preset for model laid over the one in
// config.json: provider, model, the window and the reply limit are magpie's
// alone, set in place or gone (theirsKept would keep one want no longer
// writes, so the window the model before magpie's was given would stay on a
// model magpie knows nothing of), and every other key in the block (a
// temperature) is the user's, kept.
func nanobotPresetKept(path, model string) any {
	want := nanobotPresetJSON(model)
	cur, ok := edit.GetJSON(path, nanobotPreset)
	if !ok {
		return want
	}
	var have map[string]json.RawMessage
	if json.Unmarshal([]byte(cur), &have) != nil {
		return want
	}
	for k, v := range have {
		if _, mine := want[k]; !mine && !slices.Contains(nanobotOurs, k) {
			want[k] = v
		}
	}
	return want
}

// nanobotPresetJSON is magpie's model preset for model, as schema.py's
// ModelPresetConfig reads it, with the model's window and reply limit when
// magpie knows them: without them nanobot takes every model for its own
// 200000/8192 defaults.
func nanobotPresetJSON(model string) map[string]any {
	e := map[string]any{"provider": magpieID, "model": model}
	for _, m := range magpieModels("nanobot") {
		if m.ID != model {
			continue
		}
		if m.Context > 0 {
			e["contextWindowTokens"] = m.Context
		}
		if n := maxTokens(m); n > 0 {
			e["maxTokens"] = n
		}
		break
	}
	return e
}

// nanobotPresets are the model presets in config.json by name, each with the
// model it names. A preset's name may hold a dot, which a gjson key path
// would take for a step, so the map is decoded whole rather than asked of one
// key at a time; nil when the file holds none or none it can read.
func nanobotPresets(path string) map[string]string {
	raw, ok := edit.GetJSON(path, "modelPresets")
	if !ok {
		return nil
	}
	var ps map[string]struct {
		Model string `json:"model"`
	}
	if json.Unmarshal([]byte(raw), &ps) != nil {
		return nil
	}
	out := make(map[string]string, len(ps))
	for n, p := range ps {
		out[n] = p.Model
	}
	return out
}

// nanobotOwn are the model presets of the user's own in config.json, by
// name, for the picker: the values the field takes for them.
func nanobotOwn(path, cur string) []Option {
	ps := nanobotPresets(path)
	names := make([]string, 0, len(ps))
	for n := range ps {
		if n != magpieID {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	out := make([]Option, 0, len(names)+1)
	for _, n := range names {
		out = append(out, Option{Value: n, Note: ps[n], Icon: modelIcon("", ps[n])})
	}
	if cur != "" && !prefixed(cur) {
		if _, listed := ps[cur]; !listed {
			out = append([]Option{{Value: cur, Icon: modelIcon("", cur)}}, out...)
		}
	}
	return group("nanobot", out)
}
