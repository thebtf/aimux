// Package upgrade test exports — exposes unexported symbols for external test packages.
package upgrade

// AtomicReplaceBinaryForTest exposes atomicReplaceBinary for use in external
// test packages (package upgrade_test). This file is compiled only during testing.
var AtomicReplaceBinaryForTest = atomicReplaceBinary
