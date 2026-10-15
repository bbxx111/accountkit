package smtp

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"strings"
	"unicode/utf8"

	"github.com/bbxx111/accountkit/user/sender"
)

func message(from, to string, content EmailContent) ([]byte, error) {
	if strings.TrimSpace(content.Subject) == "" || strings.TrimSpace(content.Text) == "" || strings.ContainsAny(content.Subject, "\r\n") {
		return nil, sender.ErrUnavailable
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\n", from, to, encodeSubject(content.Subject))
	if content.HTML == "" {
		buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
		if err := writeBody(&buf, content.Text); err != nil {
			return nil, sender.ErrUnavailable
		}
	} else {
		alternatives := multipart.NewWriter(&buf)
		fmt.Fprintf(&buf, "Content-Type: %s\r\n\r\n", mime.FormatMediaType("multipart/alternative", map[string]string{"boundary": alternatives.Boundary()}))
		for _, part := range []struct{ kind, body string }{{"text/plain", content.Text}, {"text/html", content.HTML}} {
			header := make(textproto.MIMEHeader)
			header.Set("Content-Type", part.kind+"; charset=UTF-8")
			header.Set("Content-Transfer-Encoding", "quoted-printable")
			writer, err := alternatives.CreatePart(header)
			if err != nil {
				return nil, sender.ErrUnavailable
			}
			if err = writeBody(writer, part.body); err != nil {
				return nil, sender.ErrUnavailable
			}
		}
		if err := alternatives.Close(); err != nil {
			return nil, sender.ErrUnavailable
		}
	}
	return buf.Bytes(), nil
}

func writeBody(destination io.Writer, text string) error {
	writer := quotedprintable.NewWriter(destination)
	if _, err := io.WriteString(writer, text); err != nil {
		return err
	}
	return writer.Close()
}

// encodeSubject 仅在完整 RFC 2047 encoded-word 之间折行。ASCII 同样编码，
// 避免折行改变原始空白，并允许拆分无空格的长标题。每段最多 42 个 UTF-8
// 字节，编码后最多 68 字节；加上首行的 "Subject: " 仍不超过 78 字节。
func encodeSubject(subject string) string {
	var header strings.Builder
	for subject != "" {
		end := 0
		for end < len(subject) {
			_, size := utf8.DecodeRuneInString(subject[end:])
			if end+size > 42 {
				break
			}
			end += size
		}
		if header.Len() != 0 {
			header.WriteString("\r\n ")
		}
		header.WriteString("=?UTF-8?b?")
		header.WriteString(base64.StdEncoding.EncodeToString([]byte(subject[:end])))
		header.WriteString("?=")
		subject = subject[end:]
	}
	return header.String()
}
