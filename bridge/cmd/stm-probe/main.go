// Command stm-probe is a diagnostic for the PHC STM v3 transport. It performs
// service.stm.whoAreYou against a real STM and reports a redacted identity,
// the round-trip time, and whether the known malformed date line was removed.
//
//	stm-probe -stm 192.168.x.x:6680 whoami
//
// It deliberately prints only a redacted identity so facility/device IDs do not
// end up in terminals or logs.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/jtschneider/PHCRemoteControl/bridge/internal/controller"
	"github.com/jtschneider/PHCRemoteControl/bridge/internal/domain"
	"github.com/jtschneider/PHCRemoteControl/bridge/internal/project"
	"github.com/jtschneider/PHCRemoteControl/bridge/internal/stm"
)

func main() {
	addr := flag.String("stm", "", "STM address, host[:port] (default port 6680)")
	timeout := flag.Duration("timeout", 10*time.Second, "overall RPC timeout")
	samples := flag.Int("samples", 1, "read-only samples for the state latency summary")
	flag.Parse()

	cmd := flag.Arg(0)
	if *addr == "" || cmd == "" {
		fmt.Fprintln(os.Stderr, "usage: stm-probe -stm HOST[:PORT] {whoami|dump|project|state}")
		os.Exit(2)
	}

	ep, err := stm.ParseEndpoint(*addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	client := stm.NewClient(ep)
	if *samples < 1 || *samples > 100 {
		fmt.Fprintln(os.Stderr, "error: samples must be between 1 and 100")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	switch cmd {
	case "whoami":
		start := time.Now()
		id, err := client.WhoAreYou(ctx)
		elapsed := time.Since(start)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Printf("STM reached at %s:%d\n", ep.Host, ep.Port)
		fmt.Printf("  round-trip:            %s\n", elapsed.Round(time.Millisecond))
		fmt.Printf("  malformed line removed: %v\n", client.LastResponseSanitized())
		fmt.Printf("  STM-Address:           %d\n", id.STMAddress)
		fmt.Printf("  Device-Name:           %s\n", redact(id.DeviceName))
		fmt.Printf("  Facility-ID:           %s\n", redact(id.FacilityID))
		fmt.Printf("  Device-ID:             %s\n", redact(id.DeviceID))
	case "dump":
		// Ground-truth raw capture for fixture reconciliation. Prints only the
		// header block; the body carries installation identity and is not shown.
		raw, err := stm.RawWhoAreYou(ctx, ep)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		sep := 4
		idx := bytes.Index(raw, []byte("\r\n\r\n"))
		if idx < 0 {
			idx = bytes.Index(raw, []byte("\n\n"))
			sep = 2
		}
		head := raw
		bodyLen := 0
		if idx >= 0 {
			head = raw[:idx+sep]
			bodyLen = len(raw) - len(head)
		}
		fmt.Printf("--- header block (%d-byte total response) ---\n%s", len(raw), head)
		fmt.Printf("--- quoted (CR/LF/spaces visible) ---\n%q\n", head)
		fmt.Printf("--- body: %d bytes (hidden: contains installation identity) ---\n", bodyLen)

	case "project":
		// Download + extract the real project, printing only sizes/counts — never
		// any project contents (installation layout is sensitive).
		zipData, files, parsed, err := loadProject(ctx, client)
		must(err)
		fmt.Printf("project downloaded from %s:%d\n", ep.Host, ep.Port)
		fmt.Printf("  ZIP:          %d bytes\n", len(zipData))
		fmt.Printf("  project.ppfx: %d bytes\n", len(files.PPFX))
		if files.TPFX != nil {
			fmt.Printf("  project.tpfx: %d bytes\n", len(files.TPFX))
		} else {
			fmt.Printf("  project.tpfx: (absent)\n")
		}
		counts := make(map[domain.DeviceKind]int)
		deviceTotal := 0
		for _, floor := range parsed.Floors {
			for _, device := range floor.Devices {
				counts[device.Kind]++
				deviceTotal++
			}
		}
		fmt.Printf("  parsed floors:  %d\n", len(parsed.Floors))
		fmt.Printf("  parsed devices: %d\n", deviceTotal)
		kinds := make([]string, 0, len(counts))
		for kind := range counts {
			kinds = append(kinds, string(kind))
		}
		sort.Strings(kinds)
		for _, kind := range kinds {
			fmt.Printf("    %-10s %d\n", kind+":", counts[domain.DeviceKind(kind)])
		}

	case "state":
		// Read-only end-to-end controller check. Output is aggregate only: no
		// project labels, stable IDs, module addresses, or channel addresses.
		_, _, parsed, err := loadProject(ctx, client)
		must(err)
		moduleAddresses := pollableModuleAddresses(parsed)
		var directDurations []time.Duration
		for range *samples {
			for _, busAddress := range moduleAddresses {
				start := time.Now()
				response, err := client.SendTelegram(ctx, 0, busAddress, 1)
				must(err)
				if len(response) != 5 {
					must(fmt.Errorf("state reply has %d values, want 5", len(response)))
				}
				directDurations = append(directDurations, time.Since(start))
			}
		}
		control, err := controller.New(parsed, client, controller.Config{})
		must(err)
		defer control.Close()

		var sweepDurations []time.Duration
		var snapshot controller.Snapshot
		for range *samples {
			start := time.Now()
			events, unsubscribe, err := control.Subscribe(64)
			must(err)
			for control.Snapshot().Stale {
				select {
				case _, open := <-events:
					if !open {
						must(controller.ErrControllerStopped)
					}
				case <-ctx.Done():
					must(ctx.Err())
				}
			}
			sweepDurations = append(sweepDurations, time.Since(start))
			snapshot = control.Snapshot()
			unsubscribe()
		}
		powerCounts := map[controller.PowerState]int{}
		for _, state := range snapshot.Devices {
			powerCounts[state.Power]++
		}
		fmt.Printf("controller state synchronized from %s:%d\n", ep.Host, ep.Port)
		fmt.Printf("  samples:         %d sweeps, %d modules each\n", *samples, len(moduleAddresses))
		fmt.Printf("  direct AMD read: p50=%s p95=%s\n",
			percentile(directDurations, 0.50), percentile(directDurations, 0.95))
		fmt.Printf("  controller sweep: p50=%s p95=%s\n",
			percentile(sweepDurations, 0.50), percentile(sweepDurations, 0.95))
		fmt.Printf("  connection:      %s\n", snapshot.Connection)
		fmt.Printf("  state stale:     %v\n", snapshot.Stale)
		fmt.Printf("  pollable outputs: %d (on=%d off=%d unknown=%d)\n",
			len(snapshot.Devices), powerCounts[controller.PowerOn],
			powerCounts[controller.PowerOff], powerCounts[controller.PowerUnknown])

	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (try: whoami, dump, project, state)\n", cmd)
		os.Exit(2)
	}
}

func pollableModuleAddresses(parsed domain.Project) []int {
	addresses := map[int]struct{}{}
	for _, floor := range parsed.Floors {
		for _, device := range floor.Devices {
			if device.Ref.ModuleClass == domain.ModuleAMD {
				addresses[0x40|device.Ref.DIP] = struct{}{}
			}
		}
	}
	result := make([]int, 0, len(addresses))
	for address := range addresses {
		result = append(result, address)
	}
	sort.Ints(result)
	return result
}

func percentile(values []time.Duration, quantile float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]time.Duration(nil), values...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	index := int(float64(len(ordered)-1) * quantile)
	return ordered[index].Round(time.Millisecond)
}

func loadProject(ctx context.Context, client *stm.Client) ([]byte, project.Files, domain.Project, error) {
	zipData, err := project.Download(ctx, client)
	if err != nil {
		return nil, project.Files{}, domain.Project{}, err
	}
	files, err := project.Extract(zipData)
	if err != nil {
		return nil, project.Files{}, domain.Project{}, err
	}
	parsed, err := project.Parse(files.PPFX, files.TPFX)
	if err != nil {
		return nil, project.Files{}, domain.Project{}, err
	}
	return zipData, files, parsed, nil
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// redact keeps a short prefix so identities are recognisable but not disclosed.
func redact(s string) string {
	switch {
	case s == "":
		return "(empty)"
	case len(s) <= 4:
		return "****"
	default:
		return s[:4] + "…"
	}
}
