package license

// Test-only re-exports of unexported helpers. tickOnce is the test handle
// that lets ticker_test.go drive transition logic deterministically without
// spinning real timers or faking time.Now.

func TickOnceForTest(prev State) State { return tickOnce(prev) }
