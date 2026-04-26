package main

import (
	"code/crawler"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/urfave/cli/v3"
)

func main() {
	cmd := &cli.Command{
		Name:      "hexlet-go-crawler",
		Usage:     "analyze a website structure",
		UsageText: "hexlet-go-crawler [global options] [command] <url>",

		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "depth",
				Usage: "crawl depth",
				Value: 10,
			},
			&cli.IntFlag{
				Name:  "retries",
				Usage: "number of retries for failed requests",
				Value: 1,
			},
			&cli.DurationFlag{
				Name:  "delay",
				Usage: "delay between requests (example: 200ms, 1s)",
				Value: 0,
			},
			&cli.DurationFlag{
				Name:  "timeout",
				Usage: "per-request timeout",
				Value: 15000000000,
			},
			&cli.IntFlag{
				Name:  "rps",
				Usage: "limit requests per second (overrides delay)",
				Value: 0,
			},
			&cli.StringFlag{
				Name:  "user-agent",
				Usage: "custom user agent",
			},
			&cli.IntFlag{
				Name:  "workers",
				Usage: "number of concurrent workers",
				Value: 4,
			},
		},

		Action: func(ctx context.Context, cmd *cli.Command) error {
			args := cmd.Args().Slice()

			if len(args) < 1 {
				return errors.New("error: requires a URL\nExample: hexlet-go-crawler https://example.com")
			}

			httpClient := &http.Client{
				Timeout: cmd.Duration("timeout"),
			}

			res, err := crawler.Analyze(
				context.Background(),
				crawler.Options{
					URL:        args[0],
					Depth:      int32(cmd.Int("depth")),
					HTTPClient: httpClient,
					Delay:      cmd.Duration("delay"),
					RPS:        cmd.Int("rps"),
					Retries:    cmd.Int("retries"),
					UserAgent:  cmd.String("user-agent"),
					Workers:    cmd.Int("workers"),
				})
			if err != nil {
				return fmt.Errorf("%w", err)
			}

			fmt.Println(string(res))
			return nil
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
