// The trivial payload container delivered over TFTP in the smoke test.
// jag compiles this; the host capture sink saves the relocated image that
// `jag run hello.toit` would PUT. The loader then delivers + runs it, and this
// heartbeat on the serial console is the proof the delivered image ran.

main:
  n := 0
  while true:
    print "delivered tick $n"
    n++
    sleep --ms=1000
