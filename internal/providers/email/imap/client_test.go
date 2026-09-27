package imapmail

import (
	"slices"
	"strings"
	"testing"

	"github.com/emersion/go-imap/v2"
)

func TestToIMAPFlags(t *testing.T) {
	t.Parallel()
	got := toIMAPFlags([]string{"seen", "Read", " flagged ", "", "\\Answered", "$Custom"})
	want := []imap.Flag{imap.FlagSeen, imap.FlagSeen, imap.FlagFlagged, imap.FlagAnswered, "$Custom"}
	if !slices.Equal(got, want) {
		t.Fatalf("flags = %v, want %v", got, want)
	}
}

func TestTruncateRunesKeepsValidUTF8(t *testing.T) {
	t.Parallel()
	got, truncated := truncateRunes("aé", 2)
	if got != "a" || !truncated {
		t.Fatalf("got %q, truncated = %v", got, truncated)
	}
	got, truncated = truncateRunes("abc", 10)
	if got != "abc" || truncated {
		t.Fatalf("got %q, truncated = %v", got, truncated)
	}
}

func TestParseMIMESeparatesBodiesAndAttachments(t *testing.T) {
	t.Parallel()
	raw := strings.ReplaceAll(`From: a@example.com
To: b@example.com
Subject: =?ISO-8859-1?Q?Relat=F3rio?=
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="outer"

--outer
Content-Type: multipart/alternative; boundary="alt"

--alt
Content-Type: text/plain; charset=ISO-8859-1
Content-Transfer-Encoding: quoted-printable

Ol=E1 mundo
--alt
Content-Type: text/html; charset=utf-8

<p>Olá mundo</p>
--alt--
--outer
Content-Type: image/png
Content-Disposition: inline

PNGDATA
--outer
Content-Type: application/pdf
Content-Disposition: attachment; filename="nota.pdf"

PDFDATA
--outer--
`, "\n", "\r\n")

	text, html, truncated, attachments := parseMIME([]byte(raw))
	if strings.TrimSpace(text) != "Olá mundo" {
		t.Fatalf("text = %q", text)
	}
	if strings.TrimSpace(html) != "<p>Olá mundo</p>" {
		t.Fatalf("html = %q", html)
	}
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if len(attachments) != 2 || attachments[0].MIMEType != "image/png" || attachments[1].Filename != "nota.pdf" {
		t.Fatalf("attachments = %+v", attachments)
	}
}

func TestDefaultFolder(t *testing.T) {
	t.Parallel()
	if defaultFolder("  ") != "INBOX" || defaultFolder(" Archive ") != "Archive" {
		t.Fatal("defaultFolder mismatch")
	}
}
