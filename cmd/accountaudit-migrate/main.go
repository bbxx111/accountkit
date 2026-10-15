package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/bbxx111/accountkit/audit/authentication"
	"github.com/bbxx111/accountkit/internal/auditmigration"
	"github.com/bbxx111/accountkit/migrations"
	"github.com/jackc/pgx/v5"
	"io"
	"os"
	"os/signal"
	"regexp"
	"time"
)

func run(args []string, getenv func(string) string, out io.Writer) int {
	f := flag.NewFlagSet("accountaudit-migrate", flag.ContinueOnError)
	f.SetOutput(io.Discard)
	var o auditmigration.Options
	var dsnEnv string
	f.StringVar(&dsnEnv, "dsn-env", "", "name of environment variable containing the database DSN")
	f.StringVar(&o.Schema, "schema", "", "target schema")
	f.StringVar(&o.ConfirmSchema, "confirm-schema", "", "repeat the exact target schema")
	f.StringVar(&o.Source, "source", "", "stable audit source")
	f.StringVar(&o.Namespace, "namespace", "", "stable account/resource namespace")
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		fmt.Fprintln(out, "audit preparation: invalid arguments")
		return 2
	}
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(dsnEnv) || !migrations.ValidSchema(o.Schema) || o.Schema != o.ConfirmSchema || (authentication.Options{Source: o.Source, Namespace: o.Namespace}).Validate() != nil {
		fmt.Fprintln(out, "audit preparation: explicit DSN environment variable, schema confirmation, source and namespace are required")
		return 2
	}
	dsn := getenv(dsnEnv)
	if dsn == "" {
		fmt.Fprintln(out, "audit preparation: DSN environment variable is empty")
		return 2
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	connecting, stop := context.WithTimeout(ctx, 15*time.Second)
	c, err := pgx.Connect(connecting, dsn)
	stop()
	if err != nil {
		fmt.Fprintln(out, "audit preparation: database connection failed")
		return 1
	}
	defer func() {
		closing, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = c.Close(closing)
	}()
	report, err := auditmigration.Prepare(ctx, c, o)
	if err != nil {
		fmt.Fprintln(out, "audit preparation failed; original rows and migration version were not changed")
		return 1
	}
	fmt.Fprintf(out, "audit preparation complete: prepared=%d reused=%d\n", report.Prepared, report.Reused)
	return 0
}
func main() { os.Exit(run(os.Args[1:], os.Getenv, os.Stdout)) }
