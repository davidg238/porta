// device/config_store.toit — the node's per-app config (setpoints) store. The
// config is one NVS blob: {appName: {key: value}}, separate from the goal/triggers
// plane. The supervisor writes it when a `set` command is drained; the
// ControlServiceProvider reads it for apps. Map helpers are pure (host-testable);
// load/save wrap the NVS bucket.
import system.storage

/** NVS key (in the supervisor's bucket) holding the {app:{key:value}} config blob. */
CONFIG-KEY ::= "config"

/** Sets app $app's $key to $value in the in-memory config map $config (creates the app sub-map). */
set-config config/Map app/string key/string value -> none:
  (config.get app --init=: {:})[key] = value

/** Returns app $app's $key from $config, or null if the app or key is absent. */
get-config config/Map app/string key/string -> any:
  app-map := config.get app --if-absent=: return null
  return app-map.get key

/** Loads the config blob from NVS, or an empty map if none stored yet. */
load-config bucket/storage.Bucket -> Map:
  return bucket.get CONFIG-KEY --if-absent=: {:}

/** Persists the config blob to NVS. */
save-config bucket/storage.Bucket config/Map -> none:
  bucket[CONFIG-KEY] = config
