// gateway-extension installs and verifies versioned executable workload
// extensions. Runtime workers only load explicitly configured local paths.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/webong/gateway/src/spinner/extensions/catalog"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gateway-extension:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("use catalog, keygen, sign, list, install, or verify")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch args[0] {
	case "keygen":
		flags := flag.NewFlagSet("keygen", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		private := flags.String("private-key", "", "new private Ed25519 key file")
		public := flags.String("public-key", "", "new public Ed25519 key file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *private == "" || *public == "" || flags.NArg() != 0 {
			return fmt.Errorf("keygen requires --private-key, --public-key and no positional arguments")
		}
		return catalog.GenerateSigningKeys(*private, *public)
	case "sign":
		flags := flag.NewFlagSet("sign", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		reference := flags.String("catalog", "", "local catalog.json path")
		private := flags.String("private-key", "", "private Ed25519 key file")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *reference == "" || *private == "" || flags.NArg() != 0 {
			return fmt.Errorf("sign requires --catalog, --private-key and no positional arguments")
		}
		path, err := catalog.SignCatalog(*reference, *private)
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
	case "catalog":
		flags := flag.NewFlagSet("catalog", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		dir := flags.String("dir", "", "directory containing gateway-* extension executables")
		version := flags.String("version", "", "exact vMAJOR.MINOR.PATCH version")
		layout := flags.String("layout", "local", "local or platform (OS/architecture subdirectories)")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *dir == "" || *version == "" || flags.NArg() != 0 {
			return fmt.Errorf("catalog requires --dir, --version and no positional arguments")
		}
		var path string
		var err error
		switch *layout {
		case "local":
			path, err = catalog.CreateLocal(*dir, *version)
		case "platform":
			path, err = catalog.CreatePlatformBundle(*dir, *version)
		default:
			return fmt.Errorf("unsupported catalog layout %q; use local or platform", *layout)
		}
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
	case "list":
		flags := flag.NewFlagSet("list", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		reference := flags.String("catalog", "", "trusted local path or HTTPS catalog URL")
		public := flags.String("public-key", "", "pinned Ed25519 public key file; require a valid detached signature")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *reference == "" || flags.NArg() != 0 {
			return fmt.Errorf("list requires --catalog and no positional arguments")
		}
		var manifest catalog.Catalog
		var err error
		if *public != "" {
			key, keyErr := catalog.LoadPublicKey(*public)
			if keyErr != nil {
				return keyErr
			}
			manifest, err = catalog.ReadSigned(ctx, *reference, key, nil)
		} else {
			manifest, err = catalog.Read(ctx, *reference, nil)
		}
		if err != nil {
			return err
		}
		sort.Slice(manifest.Artifacts, func(i, j int) bool {
			left, right := manifest.Artifacts[i], manifest.Artifacts[j]
			return left.Name+"\x00"+left.Version+"\x00"+left.OS+"\x00"+left.Arch < right.Name+"\x00"+right.Version+"\x00"+right.OS+"\x00"+right.Arch
		})
		for _, artifact := range manifest.Artifacts {
			fmt.Printf("%s\t%s\t%s/%s\t%s\n", artifact.Name, artifact.Version, artifact.OS, artifact.Arch, artifact.SHA256)
		}
		return nil
	case "install":
		flags := flag.NewFlagSet("install", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		reference := flags.String("catalog", "", "trusted local path or HTTPS catalog URL")
		name := flags.String("name", "", "extension name")
		version := flags.String("version", "", "exact vMAJOR.MINOR.PATCH version")
		dir := flags.String("dir", "", "absolute installation directory")
		public := flags.String("public-key", "", "pinned Ed25519 public key file; require a valid detached signature")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *reference == "" || *name == "" || *version == "" || *dir == "" || flags.NArg() != 0 {
			return fmt.Errorf("install requires --catalog, --name, --version, --dir and no positional arguments")
		}
		var key []byte
		if *public != "" {
			loaded, err := catalog.LoadPublicKey(*public)
			if err != nil {
				return err
			}
			key = loaded
		}
		path, err := catalog.Install(ctx, catalog.InstallRequest{Catalog: *reference, Name: *name, Version: *version, Dir: *dir, PublicKey: key})
		if err != nil {
			return err
		}
		fmt.Println(path)
		return nil
	case "verify":
		flags := flag.NewFlagSet("verify", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		path := flags.String("path", "", "absolute installed executable path")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *path == "" || flags.NArg() != 0 {
			return fmt.Errorf("verify requires --path and no positional arguments")
		}
		receipt, err := catalog.VerifyInstalled(*path)
		if err != nil {
			return err
		}
		fmt.Printf("verified %s %s %s/%s %s\n", receipt.Name, receipt.Version, receipt.OS, receipt.Arch, receipt.SHA256)
		return nil
	default:
		return fmt.Errorf("unknown command %q; use catalog, keygen, sign, list, install, or verify", args[0])
	}
}
