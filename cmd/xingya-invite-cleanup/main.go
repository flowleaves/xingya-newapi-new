// Command xingya-invite-cleanup closes the retired deferred-invite ledger.
//
// It is an operator tool, not part of the server startup path. Run it with the
// service stopped, after taking a database backup. The default mode is a dry run.
//
//	XINGYA_INVITE_CLEANUP_APPLY=1 go run ./cmd/xingya-invite-cleanup -apply -yes
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/joho/godotenv"
)

func main() {
	apply := flag.Bool("apply", false, "settle eligible rewards and cancel pending rewards")
	confirm := flag.Bool("yes", false, "confirm the destructive write; required with -apply")
	flag.Parse()

	if err := godotenv.Load(".env"); err != nil {
		fmt.Fprintln(os.Stderr, "no .env file found; using process environment")
	}
	common.InitEnv()
	logger.SetupLogger()

	if *apply && !*confirm {
		fmt.Fprintln(os.Stderr, "refusing to write without -yes; run the dry run first")
		os.Exit(2)
	}
	if err := model.InitDB(); err != nil {
		fmt.Fprintln(os.Stderr, "failed to initialize database:", err)
		os.Exit(1)
	}
	if common.RedisEnabled {
		if err := common.InitRedisClient(); err != nil {
			fmt.Fprintln(os.Stderr, "failed to initialize redis (quota cache will expire naturally):", err)
		}
	}

	summary, err := model.SettleLegacyInviteRewardsForRemoval(*apply)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cleanup failed:", err)
		os.Exit(1)
	}
	encoded, err := common.Marshal(summary)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cleanup summary: %+v\n", summary)
	} else {
		fmt.Println(string(encoded))
	}
	if !*apply {
		fmt.Println("dry run only; re-run with -apply -yes after verifying the counts and taking a backup")
	}
}
