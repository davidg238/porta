// device/triggers.toit
/**
Artemis-compatible container triggers, parsed from a goal-state trigger map of
  the form {"boot":1, "interval":60, "gpio-high:33":33, "gpio-touch:4":4}.
Reference: artemis/src/cli/pod-specification.toit:761-912.
*/
class Triggers:
  boot/bool
  install/int?
  interval-s/int?
  gpio-high/List
  gpio-low/List
  touch/List

  constructor --.boot=false --.install=null --.interval-s=null
      --.gpio-high=[] --.gpio-low=[] --.touch=[]:

  /** Parses the goal-state {type:value} trigger map. */
  static parse map/Map -> Triggers:
    boot := false
    install/int? := null
    interval-s/int? := null
    gh/List := []
    gl/List := []
    gt/List := []
    map.do: | key/string value |
      if key == "boot": boot = true
      else if key == "install": install = value
      else if key == "interval": interval-s = value
      else if key.starts-with "gpio-high:": gh.add (int.parse key[10..])
      else if key.starts-with "gpio-low:": gl.add (int.parse key[9..])
      else if key.starts-with "gpio-touch:": gt.add (int.parse key[11..])
      else: throw "unknown trigger: $key"
    result := Triggers --boot=boot --install=install --interval-s=interval-s --gpio-high=gh --gpio-low=gl --touch=gt
    return result

  /** Serializes back to the goal-state {type:value} map form. */
  to-map -> Map:
    m := {:}
    if boot: m["boot"] = 1
    if install != null: m["install"] = install
    if interval-s != null: m["interval"] = interval-s
    gpio-high.do: m["gpio-high:$it"] = it
    gpio-low.do: m["gpio-low:$it"] = it
    touch.do: m["gpio-touch:$it"] = it
    return m

  /** Pin mask for esp32.enable-external-wakeup covering all gpio-high pins. */
  ext1-high-mask -> int:
    mask := 0
    gpio-high.do: mask |= 1 << it
    return mask
