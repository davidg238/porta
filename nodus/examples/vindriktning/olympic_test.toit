import expect show *
import .olympic show olympic-mean

main:
  // Drops one high + one low, averages the middle.
  expect-equals 4.0 (olympic-mean [1, 2, 3, 4, 5, 6, 7])     // drop 1 & 7 -> mean(2..6)
  // Eight samples -> middle six (the vin case).
  expect-equals 4.5 (olympic-mean [1, 2, 3, 4, 5, 6, 7, 8])  // drop 1 & 8 -> mean(2..7)
  // A single high spike is trimmed away.
  expect-equals 10.0 (olympic-mean [10, 10, 10, 10, 999])    // drop 999 & one 10
  // Unsorted input is handled.
  expect-equals 4.5 (olympic-mean [8, 1, 5, 3, 7, 2, 6, 4])
  // Minimum size (3 -> single middle element).
  expect-equals 2.0 (olympic-mean [1, 2, 3])
  // Fewer than 3 throws.
  expect-throw "olympic-mean needs >= 3 values": olympic-mean [1, 2]
  print "olympic OK"
