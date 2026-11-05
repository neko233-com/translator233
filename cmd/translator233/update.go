package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/neko233-com/translator233/internal/update"
)

// cmdUpdate checks GitHub for a newer release and installs it.
func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "只检查，不安装")
	asJSON := fs.Bool("json", false, "JSON 输出")
	if err := fs.Parse(reorder(args)); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	rel, err := update.Latest(ctx)
	if err != nil {
		return err
	}
	plan, err := update.Prepare(version, rel)
	if err != nil {
		return err
	}
	if plan == nil {
		if *asJSON {
			json.NewEncoder(os.Stdout).Encode(map[string]any{
				"current": version, "latest": rel.Version(), "updated": false,
			})
			return nil
		}
		fmt.Printf("已是最新版本 %s\n", version)
		return nil
	}

	if *check {
		if *asJSON {
			json.NewEncoder(os.Stdout).Encode(map[string]any{
				"current": version, "latest": plan.Latest, "updated": true,
				"asset": plan.Asset.Name, "url": plan.Release.HTMLURL,
			})
			return nil
		}
		fmt.Printf("当前 %s，最新 %s\n%s\n", version, plan.Latest, plan.Release.HTMLURL)
		return nil
	}

	fmt.Fprintf(os.Stderr, "正在更新 %s → %s（%s）…\n", version, plan.Latest, plan.Asset.Name)
	path, err := plan.Download(ctx)
	if err != nil {
		return err
	}
	if want, ok := plan.Checksums(ctx); ok {
		if err := update.VerifySHA256(path, want); err != nil {
			os.Remove(path)
			return err
		}
	}
	if err := update.Install(ctx, path, plan.Installer); err != nil {
		return err
	}
	if *asJSON {
		json.NewEncoder(os.Stdout).Encode(map[string]any{
			"current": version, "latest": plan.Latest, "updated": true, "asset": plan.Asset.Name,
		})
	} else {
		fmt.Println(update.RelaunchHint())
	}
	return nil
}
