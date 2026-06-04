// Copyright (c) 2026 Ekorau LLC

package control

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/davidg238/porta/internal/config"
	"github.com/davidg238/porta/internal/store"
)

// RelativeAge renders an epoch-seconds timestamp relative to now.
func RelativeAge(ts, now int64) string {
	if ts == 0 {
		return "never"
	}
	d := now - ts
	switch {
	case d <= 60:
		return fmt.Sprintf("%ds ago", d)
	case d <= 3600:
		return fmt.Sprintf("%dm ago", d/60)
	case d < 86400:
		return fmt.Sprintf("%dh ago", d/3600)
	default:
		return fmt.Sprintf("%dd ago", d/86400)
	}
}

// App is one entry from a node's observed apps map.
type App struct {
	Name     string
	CRC      int64
	Runlevel int64
}

// AppsFromObserved decodes the apps map from a cached observed_state JSON blob.
func AppsFromObserved(observed string) ([]App, error) {
	if observed == "" {
		return nil, nil
	}
	var obj struct {
		Apps map[string]struct {
			CRC      int64 `json:"crc"`
			Runlevel int64 `json:"runlevel"`
		} `json:"apps"`
	}
	if err := json.Unmarshal([]byte(observed), &obj); err != nil {
		return nil, err
	}
	var out []App
	for name, a := range obj.Apps {
		out = append(out, App{Name: name, CRC: a.CRC, Runlevel: a.Runlevel})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ConfigFromObserved decodes a node's cached observed_state JSON into the
// app→{key:value} map for config display + comparison. Uses UseNumber() so
// values match the desired side under EqualScalars.
func ConfigFromObserved(observed string) map[string]map[string]any {
	if observed == "" {
		return map[string]map[string]any{}
	}
	var obj struct {
		Config map[string]map[string]any `json:"config"`
	}
	dec := json.NewDecoder(strings.NewReader(observed))
	dec.UseNumber()
	if err := dec.Decode(&obj); err != nil || obj.Config == nil {
		return map[string]map[string]any{}
	}
	return obj.Config
}

// ConfigRow is one desired-vs-observed row for an app's key.
type ConfigRow struct {
	Key             string
	Desired         any
	Observed        any
	DesiredPresent  bool
	ObservedPresent bool
	Marker          string
	ReissueCount    int
}

// DesiredVsObserved computes the union of desired ∪ observed keys for app,
// each tagged via config.Marker, plus the self-heal reissue count. This is
// the shared computation behind `device get` and the web Config panel.
func DesiredVsObserved(st *store.Store, id, app string) ([]ConfigRow, error) {
	cmds, err := st.CommandLog(id)
	if err != nil {
		return nil, err
	}
	n, err := st.GetNode(id)
	if err != nil {
		return nil, err
	}
	desired := config.ProjectDesiredForApp(cmds, app)
	observed := map[string]any{}
	if n != nil {
		if c := ConfigFromObserved(n.ObservedState)[app]; c != nil {
			observed = c
		}
	}
	seen := map[string]struct{}{}
	for k := range desired {
		seen[k] = struct{}{}
	}
	for k := range observed {
		seen[k] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]ConfigRow, 0, len(keys))
	for _, k := range keys {
		d, dOK := desired[k]
		o, oOK := observed[k]
		out = append(out, ConfigRow{
			Key: k, Desired: d, Observed: o, DesiredPresent: dOK, ObservedPresent: oOK,
			Marker:       config.Marker(d, o, dOK, oOK),
			ReissueCount: config.ReconcileCount(cmds, app, k),
		})
	}
	return out, nil
}
