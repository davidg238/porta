import expect show *
import .command show *

main:
  // run round-trips through the wire encoding.
  c := Command.run --name="blink" --crc=7 --size=4096 --triggers={"interval": 30}
  bytes := c.encode
  expect (bytes.size >= 1)            // the "real command ≥ 1 byte" invariant
  d := Command.decode bytes
  expect-equals VERB-RUN d.verb
  expect-equals "blink" d.name
  expect-equals 7 d.crc
  expect-equals 30 d.triggers["interval"]
  expect-equals 3 d.runlevel          // default
  expect-equals [] d.arguments        // default

  // stop.
  s := Command.decode (Command.stop --name="blink").encode
  expect-equals VERB-STOP s.verb
  expect-equals "blink" s.name

  // set-poll-interval.
  p := Command.decode (Command.set-poll-interval --interval-s=5).encode
  expect-equals VERB-SET-POLL-INTERVAL p.verb
  expect-equals 5 p.interval-s

  // triggers-from-flags builds the device {type:value} trigger map.
  t := triggers-from-flags ["boot", "gpio-high=33"] --interval-s=30
  expect-equals 1 t["boot"]
  expect-equals 33 t["gpio-high:33"]
  expect-equals 30 t["interval"]
  expect-throw "unknown trigger: bogus": triggers-from-flags ["bogus"] --interval-s=null

  // project folds a command list to the goal-app map; it is idempotent.
  run-x := Command.run --name="x" --crc=1 --size=512 --triggers={"interval": 10}
  expect-structural-equals (project [run-x]) (project [run-x, run-x])      // re-run is a no-op
  expect (project [run-x, (Command.stop --name="x")]).is-empty  // stop removes
  later := project [run-x, (Command.run --name="x" --crc=2 --size=1024 --triggers={:})]
  expect-equals 2 later["x"]["crc"]                             // later run wins

  // run carries size so the device can size its image writer from the command alone.
  rc := Command.run --name="blink" --crc=999 --size=2048 --triggers={"interval": 30}
  expect-equals 2048 rc.size
  decoded := Command.decode rc.encode
  expect-equals 2048 decoded.size
  expect-equals 999 decoded.crc

  // set round-trips through the wire encoding (typed value preserved).
  st := Command.decode (Command.set --app="thermostat" --key="target-c" --value=21.5).encode
  expect-equals VERB-SET st.verb
  expect-equals "thermostat" st.app
  expect-equals "target-c" st.config-key
  expect-equals 21.5 st.config-value
  expect (st.config-value is float)
  // int / bool / string values survive too.
  expect (Command.decode (Command.set --app="a" --key="n" --value=7).encode).config-value is int
  expect-equals true (Command.decode (Command.set --app="a" --key="on" --value=true).encode).config-value
  expect-equals "heat" (Command.decode (Command.set --app="a" --key="mode" --value="heat").encode).config-value

  // project-config folds set commands to {app:{key:value}}, last-write-wins, by app.
  cfg := project-config [
    Command.set --app="t" --key="target" --value=20,
    Command.set --app="t" --key="mode" --value="heat",
    Command.set --app="t" --key="target" --value=22,   // overwrites
    Command.set --app="s" --key="threshold" --value=100,
  ]
  expect-equals 22 cfg["t"]["target"]
  expect-equals "heat" cfg["t"]["mode"]
  expect-equals 100 cfg["s"]["threshold"]
  // a set does not appear in the goal-app projection (separate plane).
  expect (project [Command.set --app="t" --key="x" --value=1]).is-empty

  // infer-scalar types a CLI string.
  expect-equals true (infer-scalar "true")
  expect-equals false (infer-scalar "false")
  expect (infer-scalar "42") is int
  expect-equals 42 (infer-scalar "42")
  expect (infer-scalar "1.5") is float
  expect-equals 1.5 (infer-scalar "1.5")
  expect-equals "auto" (infer-scalar "auto")
  expect-equals -42 (infer-scalar "-42")
  expect (infer-scalar "-42") is int
  expect-equals "nan" (infer-scalar "nan")     // not a decimal → stays a string
  expect-equals "inf" (infer-scalar "inf")

  // config-marker classifies a key across desired/observed.
  cd := {"setpoint": 21.5, "mode": "eco", "hyst": 0.5}
  co := {"setpoint": 21.5, "mode": "heat"}
  expect-equals "" (config-marker cd co "setpoint")        // present & equal
  expect-equals "(drift)" (config-marker cd co "mode")     // present & unequal
  expect-equals "(pending)" (config-marker cd co "hyst")   // desired only
  expect-equals "" (config-marker cd co "extra")           // neither present
  expect-equals "" (config-marker {:} {"x": 1} "x")        // observed only → no marker

  // render-config-table emits a header + one row per union key, "--" for absent cells.
  lines := render-config-table "thermostat" cd co
  joined := lines.join "\n"
  expect (joined.contains "config for thermostat")
  expect (joined.contains "setpoint")
  expect (joined.contains "21.5")
  expect (joined.contains "(drift)")     // mode row
  expect (joined.contains "(pending)")   // hyst row
  expect (joined.contains "--")          // hyst observed cell
  // Empty on both sides → a single "no config" line.
  empty-lines := render-config-table "x" {:} {:}
  expect-equals 1 empty-lines.size
  expect (empty-lines[0].contains "no config")
  // observed-only key still appears (abnormal but rendered).
  oo := render-config-table "a" {:} {"k": 9}
  expect ((oo.join "\n").contains "k")

  // --- reconcile-config: diff desired (set log) vs observed, re-issue divergent delivered sets ---
  // Build a command-log entry the way Store.command-log returns it.
  set-entry := : | app/string key/string value/any delivered/any |
    {"verb": VERB-SET, "args": {"app": app, "key": key, "value": value},
     "issued_by": "cli", "delivered_at": delivered}

  // delivered + drift (observed present but unequal) → re-issue, replayed verbatim.
  drift-log := [set-entry.call "t" "mode" "heat" 100]
  drift := reconcile-config drift-log {"t": {"mode": "eco"}}
  expect-equals 1 drift.size
  expect-equals VERB-SET drift[0].verb
  expect-structural-equals {"app": "t", "key": "mode", "value": "heat"} drift[0].args

  // delivered + pending (observed absent) → re-issue.
  pending := reconcile-config [set-entry.call "t" "mode" "heat" 100] {"t": {:}}
  expect-equals 1 pending.size

  // app entirely absent from observed → also re-issues (obs-app defaults to {:}).
  expect-equals 1 (reconcile-config [set-entry.call "t" "mode" "heat" 100] {:}).size

  // undelivered + drift → SKIP (in-flight guard).
  inflight := reconcile-config [set-entry.call "t" "mode" "heat" null] {"t": {"mode": "eco"}}
  expect (inflight.is-empty)

  // converged (delivered + equal) → skip.
  expect (reconcile-config [set-entry.call "t" "mode" "heat" 100] {"t": {"mode": "heat"}}).is-empty

  // observed-only key (no desired set) → skip (desired never shrinks).
  expect (reconcile-config [] {"t": {"ghost": 1}}).is-empty

  // multi-app/key: only divergent delivered keys re-issue.
  multi-log := [
    set-entry.call "t" "mode" "heat" 100,    // drift → reissue
    set-entry.call "t" "sp" 22 100,          // converged → skip
    set-entry.call "s" "thr" 5 100,          // pending → reissue
  ]
  multi := reconcile-config multi-log {"t": {"mode": "eco", "sp": 22}, "s": {:}}
  expect-equals 2 multi.size

  // scalar type fidelity: a float that round-trips equal does NOT re-issue (no false drift),
  // and a re-issued command's args equal the original row's args (verbatim replay).
  expect (reconcile-config [set-entry.call "t" "f" 21.5 100] {"t": {"f": 21.5}}).is-empty
  fid := reconcile-config [set-entry.call "t" "f" 21.5 100] {"t": {"f": 22.5}}
  expect (fid[0].args["value"] is float)
  expect-equals 21.5 fid[0].args["value"]

  // self-throttle: latest set for the key is an undelivered gateway-reconcile set → skip.
  throttle-log := [
    set-entry.call "t" "mode" "heat" 100,    // delivered cli set
    {"verb": VERB-SET, "args": {"app": "t", "key": "mode", "value": "heat"},
     "issued_by": "gateway-reconcile", "delivered_at": null},  // in-flight reissue (latest)
  ]
  expect (reconcile-config throttle-log {"t": {"mode": "eco"}}).is-empty

  // --- reconcile-count: how many gateway-reconcile sets targeted (app, key) ---
  count-log := [
    {"verb": VERB-SET, "args": {"app": "t", "key": "mode", "value": "heat"},
     "issued_by": "cli", "delivered_at": 1},                 // cli, not counted
    {"verb": VERB-SET, "args": {"app": "t", "key": "mode", "value": "heat"},
     "issued_by": "gateway-reconcile", "delivered_at": 2},   // counted
    {"verb": VERB-SET, "args": {"app": "t", "key": "mode", "value": "heat"},
     "issued_by": "gateway-reconcile", "delivered_at": 3},   // counted
    {"verb": VERB-SET, "args": {"app": "t", "key": "other", "value": 1},
     "issued_by": "gateway-reconcile", "delivered_at": 4},   // different key
  ]
  expect-equals 2 (reconcile-count count-log "t" "mode")
  expect-equals 1 (reconcile-count count-log "t" "other")
  expect-equals 0 (reconcile-count count-log "t" "absent")
  expect-equals 0 (reconcile-count [] "t" "mode")

  // --- config-keys: desired keys first, then observed-only keys ---
  expect-equals ["a", "b", "c"] (config-keys {"a": 1, "b": 2} {"b": 9, "c": 3})
  expect-equals ["a"] (config-keys {"a": 1} {:})
  expect-equals ["x"] (config-keys {:} {"x": 1})
  expect-structural-equals [] (config-keys {:} {:})

  // lifecycle defaults to run-once and round-trips through the wire codec.
  r0 := Command.run --name="vin" --crc=1 --size=2 --triggers={:}
  expect-equals "run-once" r0.lifecycle
  r1 := Command.run --name="vin" --crc=1 --size=2 --triggers={:} --lifecycle="run-loop"
  expect-equals "run-loop" (Command.decode r1.encode).lifecycle
  // validator
  expect (is-valid-lifecycle "run-once")
  expect (is-valid-lifecycle "run-loop")
  expect-not (is-valid-lifecycle "forever")
