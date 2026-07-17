package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/api/handlers"
	"github.com/SowinskiBraeden/ReforgerWorkshopAPI/telemetry"
)

// runMaintenanceCommand handles the CLI maintenance flags (-import-logs,
// -rebuild-aggregates). It reports true when a command ran and the process
// should exit instead of serving.
func runMaintenanceCommand(a *handlers.App) bool {
	importLogs := flag.Bool("import-logs", false, "import historical JSON logs into the telemetry database and exit")
	rebuild := flag.Bool("rebuild-aggregates", false, "rebuild all aggregate tables from raw request events and exit")
	dryRun := flag.Bool("dry-run", false, "with -import-logs: parse and count without writing")
	fromDay := flag.String("from", "", "with -import-logs: start day (YYYY-MM-DD, inclusive)")
	toDay := flag.String("to", "", "with -import-logs: end day (YYYY-MM-DD, inclusive)")
	fresh := flag.Bool("fresh", false, "with -import-logs: ignore stored file cursors and re-scan")
	flag.Parse()
	if !*importLogs && !*rebuild {
		return false
	}

	store, err := telemetry.Open(a.Config.TelemetryDBPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "telemetry database unavailable:", err)
		os.Exit(1)
	}
	defer store.Close()
	ctx := context.Background()

	if *importLogs {
		secret := a.Config.TelemetryHashSecret
		if secret == "" {
			secret = a.Config.APIKeyHashSecret
		}
		importer := telemetry.NewImporter(store, telemetry.ImporterConfig{
			HashSecret: secret,
			Rotation:   a.Config.AnonIDRotation,
		})
		summary, err := importer.ImportDir(ctx, a.Config.LogDir, telemetry.ImportOptions{
			DryRun:  *dryRun,
			FromDay: *fromDay,
			ToDay:   *toDay,
			Fresh:   *fresh,
		})
		out, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Println(string(out))
		if err != nil {
			fmt.Fprintln(os.Stderr, "import failed:", err)
			os.Exit(1)
		}
		if !*dryRun && !summary.FirstEventAt.IsZero() {
			fmt.Println("rebuilding aggregates for imported range...")
			if err := telemetry.NewAggregator(store).RebuildRange(ctx, summary.FirstEventAt, summary.LastEventAt); err != nil {
				fmt.Fprintln(os.Stderr, "aggregate rebuild failed:", err)
				os.Exit(1)
			}
		}
	}
	if *rebuild {
		if err := telemetry.NewAggregator(store).RebuildAll(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "aggregate rebuild failed:", err)
			os.Exit(1)
		}
		fmt.Println("aggregates rebuilt")
	}
	return true
}
