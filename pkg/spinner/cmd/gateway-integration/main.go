// gateway-integration installs and verifies versioned executable integrations.
// Runtime workers only load explicitly configured local paths.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/webong/gateway/arranger/integration"
	"github.com/webong/gateway/pkg/spinner/integrations/catalog"
	"github.com/webong/gateway/pkg/spinner/provision"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gateway-integration:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("use catalog, keygen, sign, list, install, verify, or probe")
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
		dir := flags.String("dir", "", "directory containing gateway-* integration executables")
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
		name := flags.String("name", "", "integration name")
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
	case "probe":
		flags := flag.NewFlagSet("probe", flag.ContinueOnError)
		flags.SetOutput(os.Stderr)
		path := flags.String("path", "", "absolute integration executable path")
		specPath := flags.String("spec", "", "server specification JSON file")
		driver := flags.String("driver", "", "local, docker, or kubernetes")
		host := flags.String("host", "127.0.0.1", "local launch host")
		port := flags.Int("port", 8080, "local launch port")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if *path == "" || *specPath == "" || *driver == "" || flags.NArg() != 0 {
			return fmt.Errorf("probe requires --path, --spec, --driver and no positional arguments")
		}
		return probe(*path, *specPath, *driver, *host, *port)
	default:
		return fmt.Errorf("unknown command %q; use catalog, keygen, sign, list, install, verify, or probe", args[0])
	}
}

func probe(path, specPath, driver, host string, port int) error {
	if driver != provision.DriverLocal && driver != provision.DriverDocker && driver != provision.DriverKubernetes {
		return fmt.Errorf("unsupported probe driver %q", driver)
	}
	if driver == provision.DriverLocal && (host == "" || port < 1 || port > 65535) {
		return fmt.Errorf("local probe requires a host and port between 1 and 65535")
	}
	file, err := os.Open(specPath)
	if err != nil {
		return fmt.Errorf("open server specification: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > integration.ProvisionMaxMessageBytes {
		return fmt.Errorf("server specification must be a bounded regular file")
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var spec provision.ServerSpec
	if err := decoder.Decode(&spec); err != nil {
		return fmt.Errorf("decode server specification: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("server specification must contain one JSON object")
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	if spec.Driver != driver {
		return fmt.Errorf("server specification driver %q does not match probe driver %q", spec.Driver, driver)
	}
	installed, err := provision.OpenIntegration(path)
	if err != nil {
		return err
	}
	defer installed.Close()
	if installed.Type() != spec.Type || !installed.Supports(driver) {
		return fmt.Errorf("integration %q does not support %s server %q", installed.Type(), driver, spec.Type)
	}
	if err := installed.Validate(spec); err != nil {
		return fmt.Errorf("validate integration configuration: %w", err)
	}
	launch, err := installed.Launch(spec, driver, host, port)
	if err != nil {
		return fmt.Errorf("derive integration launch: %w", err)
	}
	if launch.Port < 1 || launch.Port > 65535 || launch.Executable == "" ||
		(driver == provision.DriverLocal && launch.WorkingDirectory == "") ||
		(driver != provision.DriverLocal && launch.Image == "") {
		return fmt.Errorf("integration returned an incomplete %s launch specification", driver)
	}
	// Launch environment can contain credentials; never print it.
	fmt.Printf("validated %s integration for %s launch\n", installed.Type(), driver)
	return nil
}
