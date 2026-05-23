// device/inventory.toit
import uuid
import .goal_state show GoalState App
import .triggers show Triggers

/** A container currently installed on the node, as recorded in NVS. */
class InstalledApp:
  name/string
  id/uuid.Uuid   // Committed image id.
  size/int
  crc/int
  triggers/Triggers
  runlevel/int

  constructor --.name --.id --.size --.crc --.triggers --.runlevel:

/** What the supervisor must do to match a goal. */
class Reconciliation:
  to-fetch/List     // App   — new or crc-changed: download + install.
  to-schedule/List  // InstalledApp — unchanged: start from flash.
  to-remove/List    // InstalledApp — in inventory, absent from goal.

  constructor --.to-fetch --.to-schedule --.to-remove:

/** The node's persistent inventory of installed apps (NVS-encodable). */
class Inventory:
  apps/Map  // name -> InstalledApp

  constructor .apps:

  /** Returns an empty inventory. */
  static empty -> Inventory: return Inventory {:}

  /** Decodes the plain Map/List tree produced by $encode (as stored in NVS). */
  static decode tree/Map -> Inventory:
    apps := {:}
    (tree.get "apps" --if-absent=: {:}).do: | name/string m/Map |
      id := uuid.Uuid m["id"]
      trig := Triggers.parse m["triggers"]
      app := InstalledApp --name=name --id=id --size=m["size"] --crc=m["crc"] --triggers=trig --runlevel=m["runlevel"]
      apps[name] = app
    return Inventory apps

  /** Encodes the inventory to a plain Map/List tree suitable for NVS storage. */
  encode -> Map:
    m := {:}
    apps.do: | name/string a/InstalledApp |
      m[name] = {
        "id": a.id.to-byte-array,
        "size": a.size,
        "crc": a.crc,
        "triggers": a.triggers.to-map,
        "runlevel": a.runlevel,
      }
    return {"apps": m}

  /** Compares the goal against the inventory and returns what the supervisor must do. */
  reconcile goal/GoalState -> Reconciliation:
    to-fetch := []
    to-schedule := []
    to-remove := []
    goal.apps.do: | name/string app/App |
      installed/InstalledApp? := apps.get name
      if installed != null and installed.crc == app.crc:
        to-schedule.add installed
      else:
        to-fetch.add app
    apps.do: | name/string installed/InstalledApp |
      if not goal.apps.contains name: to-remove.add installed
    result := Reconciliation --to-fetch=to-fetch --to-schedule=to-schedule --to-remove=to-remove
    return result
