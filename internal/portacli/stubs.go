package portacli

import "github.com/spf13/cobra"

func newScanCmd() *cobra.Command      { return &cobra.Command{Use: "scan", RunE: noop} }
func newPingCmd() *cobra.Command      { return &cobra.Command{Use: "ping", RunE: noop} }
func newDeviceCmd() *cobra.Command    { return &cobra.Command{Use: "device", RunE: noop} }
func newContainerCmd() *cobra.Command { return &cobra.Command{Use: "container", RunE: noop} }
func newLogCmd() *cobra.Command       { return &cobra.Command{Use: "log", RunE: noop} }
func noop(*cobra.Command, []string) error { return nil }
