package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"

	"github.com/fathorMB/NidaOS/core/internal/auth"
)

func (a *app) token(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: use token list, token create NAME or token delete ID", errUsage)
	}
	switch args[0] {
	case "list", "ls":
		if len(args) > 1 {
			return fmt.Errorf("%w: token list has no arguments", errUsage)
		}
		var body struct {
			Tokens []auth.Token `json:"tokens"`
		}
		if err := a.c.Get(ctx, "/api/v1/tokens", nil, &body); err != nil {
			return err
		}
		if a.json {
			return a.writeJSON(body)
		}
		if len(body.Tokens) == 0 {
			fmt.Fprintln(a.out, "There are no tokens. Make one with: nida token create NAME")
			return nil
		}
		w := a.table()
		fmt.Fprintln(w, "ID\tNAME\tLEVEL\tSTARTS WITH\tCREATED\tLAST USED")
		for _, t := range body.Tokens {
			used := "never"
			if t.LastUsed != nil {
				used = stamp(*t.LastUsed)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s…\t%s\t%s\n", t.ID, t.Name, t.Level, t.Hint, stamp(t.Created), used)
		}
		return w.Flush()

	case "create", "new":
		fs := flag.NewFlagSet("token create", flag.ContinueOnError)
		level := fs.String("level", auth.LevelRead, "")
		rest, err := parseFlags(fs, args[1:])
		if err != nil {
			return err
		}
		if len(rest) != 1 {
			return fmt.Errorf("%w: use token create NAME [--level read|admin]", errUsage)
		}
		var created struct {
			auth.Token
			Secret string `json:"secret"`
		}
		if err := a.c.Do(ctx, http.MethodPost, "/api/v1/tokens", map[string]string{"name": rest[0], "level": *level}, &created); err != nil {
			return err
		}
		if a.json {
			return a.writeJSON(created)
		}
		fmt.Fprintf(a.out, "The token %q (level %s, id %s) is ready:\n\n  %s\n\n", created.Name, created.Level, created.ID, created.Secret)
		fmt.Fprintln(a.out, "Keep it now: nida does not show it again. Give it to a program in the header")
		fmt.Fprintln(a.out, "\"Authorization: Bearer <token>\", or in NIDA_TOKEN for nida on an other computer.")
		return nil

	case "delete", "rm":
		if len(args) != 2 {
			return fmt.Errorf("%w: use token delete ID", errUsage)
		}
		if err := a.c.Do(ctx, http.MethodDelete, "/api/v1/tokens/"+url.PathEscape(args[1]), nil, nil); err != nil {
			return err
		}
		if !a.json {
			fmt.Fprintf(a.out, "The token %s is deleted.\n", args[1])
		}
		return nil
	}
	return fmt.Errorf("%w: unknown token command %q", errUsage, args[0])
}
