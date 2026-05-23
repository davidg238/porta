// device/schedule_store.toit
import esp32
import io show LITTLE-ENDIAN

/**
Wall-clock microseconds that advance across deep-sleep. Time.monotonic-us
  (default --since-wakeup=false) already accumulates time spent in deep sleep,
  so it is a monotonic clock across sleep cycles without any additional offset.
*/
clock-us -> int:
  return Time.monotonic-us

/**
RTC-backed scheduler state. RTC user memory survives deep-sleep but NOT a cold
  power-cycle; a 4-byte magic distinguishes "valid (woke from sleep)" from
  "cold boot" (treated as last-poll = 0, forcing a poll). Layout:
    [0:4]   magic
    [4:12]  last-poll clock-us (int64, little-endian)
    [12:20] wake-count (int64, little-endian); incremented on each construction.
*/
class ScheduleStore:
  static MAGIC_ ::= 0x50_4f_52_54  // "PORT"
  rtc_/ByteArray

  constructor:
    rtc_ = esp32.rtc-user-bytes
    if (LITTLE-ENDIAN.uint32 rtc_ 0) != MAGIC_:
      // Cold boot: initialise. last-poll = 0 so the first wake polls; wakes = 0.
      LITTLE-ENDIAN.put-uint32 rtc_ 0 MAGIC_
      LITTLE-ENDIAN.put-int64 rtc_ 4 0
      LITTLE-ENDIAN.put-int64 rtc_ 12 0
    // Count this wake.
    next-wakes := (LITTLE-ENDIAN.int64 rtc_ 12) + 1
    LITTLE-ENDIAN.put-int64 rtc_ 12 next-wakes

  last-poll-us -> int:
    return LITTLE-ENDIAN.int64 rtc_ 4

  last-poll-us= value/int -> none:
    LITTLE-ENDIAN.put-int64 rtc_ 4 value

  /** Returns the cumulative wake count since the last cold boot (including this wake). */
  wakes -> int:
    return LITTLE-ENDIAN.int64 rtc_ 12
