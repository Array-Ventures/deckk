package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/holman/deckk/internal/adapter"
	_ "github.com/holman/deckk/internal/adapter/canva"
	_ "github.com/holman/deckk/internal/adapter/docsend"
	_ "github.com/holman/deckk/internal/adapter/googledrive"
	_ "github.com/holman/deckk/internal/adapter/googleslides"
	_ "github.com/holman/deckk/internal/adapter/notion"
	_ "github.com/holman/deckk/internal/adapter/papermark"
	_ "github.com/holman/deckk/internal/adapter/pdfurl"
	_ "github.com/holman/deckk/internal/adapter/pitch"
	"github.com/holman/deckk/internal/pdf"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		output           string
		provider         string
		visitorName      string
		fields           = fieldFlags{}
		passcode         string
		verificationCode string
		verificationLink string
		headful          bool
		email            string
		timeout          time.Duration
		showVersion      bool
	)
	flag.StringVar(&provider, "provider", "", "force an adapter, for example papermark for a branded domain")
	flag.StringVar(&visitorName, "name", "", "visitor name for a Papermark access form")
	flag.Var(&fields, "field", "Papermark access field identifier=value (repeatable; text fields only)")
	flag.StringVar(&output, "o", defaultOutput(), "output PDF path")
	flag.StringVar(&output, "output", defaultOutput(), "output PDF path")
	flag.BoolVar(&headful, "headful", false, "show the browser window (debugging)")
	flag.StringVar(&email, "email", "", "email to use for email-gated decks (defaults to git user.email)")
	flag.StringVar(&passcode, "passcode", "", "passcode for a protected DocSend or Papermark deck")
	flag.StringVar(&verificationCode, "verification-code", "", "DocSend or Papermark verification code (otherwise prompted when needed)")
	flag.StringVar(&verificationLink, "verification-link", "", "unopened DocSend email verification link (or - to read from stdin)")
	flag.DurationVar(&timeout, "timeout", 5*time.Minute, "total timeout")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: deckk [flags] <url>\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if showVersion {
		fmt.Println(version())
		return nil
	}

	if flag.NArg() != 1 {
		flag.Usage()
		return fmt.Errorf("expected exactly one URL argument")
	}
	rawURL := flag.Arg(0)
	parsedURL, err := url.Parse(rawURL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "https" && parsedURL.Scheme != "http") {
		return fmt.Errorf("expected an HTTP or HTTPS document URL")
	}

	a := adapter.Find(rawURL)
	if provider != "" {
		a = adapter.FindByName(provider)
		if a == nil {
			return fmt.Errorf("unknown provider %q (available: %s)", provider, adapterNames())
		}
	}
	if a == nil {
		return fmt.Errorf("no adapter matched %q (registered: %s)", rawURL, adapterNames())
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if verificationLink != "" && a.Name() != "docsend" {
		return fmt.Errorf("email-link verification is supported only for DocSend")
	}
	if verificationLink == "-" {
		var err error
		verificationLink, err = promptVerificationLink(ctx)
		if err != nil {
			return err
		}
	}

	if email == "" {
		if gitEmail, ok := gitUserEmail(); ok {
			email = gitEmail
			fmt.Fprintf(os.Stderr, "using git user.email (%s) for any email gate\n", email)
		}
	}

	fmt.Fprintf(os.Stderr, "Using %s adapter…\n", a.Name())
	opts := adapter.Options{
		Headful: headful, Email: email, Passcode: passcode, Name: visitorName, Fields: fields,
		VerificationCode:        verificationCode,
		VerificationLink:        verificationLink,
		RequestVerificationCode: promptVerificationCode,
		RequestVerificationLink: promptVerificationLink,
	}

	// Some sources (e.g. Google Slides) expose a ready-made PDF export, so
	// there's nothing to screenshot or stitch — just save the bytes.
	if (visitorName != "" || len(fields) > 0) && a.Name() != "papermark" {
		return fmt.Errorf("--name and --field are supported only for Papermark")
	}
	var pdfBytes []byte
	var slides []adapter.Slide
	if df, ok := a.(adapter.DocumentFetcher); ok {
		result, err := df.FetchDocument(ctx, rawURL, opts)
		if err != nil {
			return err
		}
		pdfBytes, slides = result.PDF, result.Slides
		if len(pdfBytes) == 0 && len(slides) == 0 {
			return fmt.Errorf("provider returned an empty document")
		}
	} else if pf, ok := a.(adapter.PDFFetcher); ok {
		data, err := pf.FetchPDF(ctx, rawURL, opts)
		if err != nil {
			return err
		}
		pdfBytes = data
		fmt.Fprintf(os.Stderr, "Fetched deck PDF directly. Writing…\n")
	}

	if pdfBytes == nil && slides == nil {
		var err error
		slides, err = a.Fetch(ctx, rawURL, opts)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Fetched %d slide(s). Writing PDF…\n", len(slides))
	}

	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return err
	}
	f, err := os.Create(output)
	if err != nil {
		return err
	}
	defer f.Close()

	if pdfBytes != nil {
		if _, err := f.Write(pdfBytes); err != nil {
			return err
		}
	} else if err := pdf.Write(f, slides); err != nil {
		return err
	}

	fmt.Printf("PDF saved to %s\n", output)
	return nil
}

// gitUserEmail returns the configured git user.email, if any. Used as a
// reasonable default for email-gated decks so users rarely have to pass
// --email by hand.
func gitUserEmail() (string, bool) {
	out, err := exec.Command("git", "config", "--get", "user.email").Output()
	if err != nil {
		return "", false
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return "", false
	}
	return s, true
}

func defaultOutput() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "deck.pdf"
	}
	return filepath.Join(home, "Desktop", "deck.pdf")
}

// version reports the module version baked into the binary by `go install`
// or `go build` of a tagged module. Returns "dev" when no version is
// available (e.g. plain `go build` inside the repo).
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}

func adapterNames() string {
	names := ""
	for i, a := range adapter.All() {
		if i > 0 {
			names += ", "
		}
		names += a.Name()
	}
	if names == "" {
		return "(none)"
	}
	return names
}

// Keep the same browser session alive while the user retrieves their code.
func promptVerificationCode(ctx context.Context) (string, error) {
	fmt.Fprint(os.Stderr, "The provider sent a verification code to your email. Enter code: ")
	return readVerificationCode(ctx, os.Stdin)
}

func promptVerificationLink(ctx context.Context) (string, error) {
	fmt.Fprint(os.Stderr, "Paste the newest unopened DocSend verification link (blank to stop): ")
	return readVerificationCode(ctx, os.Stdin)
}

func readVerificationCode(ctx context.Context, input io.Reader) (string, error) {
	type result struct {
		code string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code, err := bufio.NewReader(input).ReadString('\n')
		if strings.TrimSpace(code) != "" {
			err = nil
		}
		done <- result{strings.TrimSpace(code), err}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-done:
		if r.err != nil {
			return "", fmt.Errorf("read verification code: %w", r.err)
		}
		if r.code == "" {
			return "", fmt.Errorf("verification code is empty")
		}
		return r.code, nil
	}
}

type fieldFlags map[string]string

func (f fieldFlags) String() string { return "" }
func (f fieldFlags) Set(raw string) error {
	key, value, ok := strings.Cut(raw, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return fmt.Errorf("expected field identifier=value")
	}
	for _, reserved := range []string{"agreement", "email", "password", "name", "code"} {
		if strings.EqualFold(key, reserved) {
			return fmt.Errorf("reserved access field; use its dedicated option or handle agreements manually")
		}
	}
	if _, exists := f[key]; exists {
		return fmt.Errorf("duplicate access field identifier")
	}
	f[key] = value
	return nil
}
