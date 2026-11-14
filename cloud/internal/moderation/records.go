package moderation

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Hamza-Labs-Core/Maktaba/cloud/internal/mail"
)

// The mails the queue sends, each in English then Arabic (the statement of reasons, the strike
// warning, the end of broadcasting, a restore, and the claimant's copy of a counter-notice). The
// wording is a template counsel reviews (README §14.9): the code only fills it in.
var mails = map[string][2]string{
	"takedown": {
		"Subject: a programme was taken off your channel\n\n" +
			"\"{programme}\" was taken off your channel after a copyright notice ({claimant}). Scope: {scope}.\n" +
			"You can respond with a counter-notice from the Broadcast page of your home.",
		"أُزيل \"{programme}\" من قناتك بعد إشعار بشأن حقوق النشر ({claimant}). النطاق: {scope}.\n" +
			"يمكنك الرد بإشعار مضاد من صفحة البث في منزلك.",
	},
	"strike_warning": {
		"Your channels have {count} valid copyright notices in the last 12 months. At {limit}, broadcasting ends for this account.",
		"تلقّت قنواتك {count} إشعارات صحيحة بشأن حقوق النشر خلال آخر 12 شهرًا. عند بلوغ {limit} ينتهي البث لهذا الحساب.",
	},
	"terminated": {
		"Broadcasting has ended for this account under our repeat-infringer policy. Remote play is not affected.\n" +
			"An appeal is by email to the designated agent (copyright@maktaba.tv).",
		"انتهى البث لهذا الحساب وفق سياستنا بشأن المخالفات المتكررة. لا يتأثر التشغيل عن بُعد.\n" +
			"يكون الطعن بالبريد الإلكتروني إلى الوكيل المعيّن (copyright@maktaba.tv).",
	},
	"restored": {
		"\"{programme}\" can air on your channel again.",
		"يمكن أن يُعرض \"{programme}\" على قناتك مجددًا.",
	},
	"counter_notice_copy": {
		"The publisher sent a counter-notice to your notice {notice_id}.\nName: {name}\nAddress: {address}\nStatement: {explanation}\n" +
			"Unless you tell the designated agent (copyright@maktaba.tv) that you have filed an action in court, the material may be " +
			"restored after {restore_after}.",
		"أرسل الناشر إشعارًا مضادًا لإشعارك {notice_id}.\nالاسم: {name}\nالعنوان: {address}\nالبيان: {explanation}\n" +
			"ما لم تُبلغ الوكيل المعيّن (copyright@maktaba.tv) بأنك رفعت دعوى أمام المحكمة، يمكن إعادة المادة بعد {restore_after}.",
	},
}

// MailText is a mail's body in both languages with its params filled in.
func MailText(kind string, params map[string]any) (string, bool) {
	t, ok := mails[kind]
	if !ok {
		return "", false
	}
	fill := func(s string) string {
		for k, v := range params {
			val := fmt.Sprint(v)
			if k == "claimant" && val == "" {
				val = "—"
			}
			s = strings.ReplaceAll(s, "{"+k+"}", val)
		}
		return strings.ReplaceAll(s, "{claimant}", "—")
	}
	return fill(t[0]) + "\n\n———\n\n" + fill(t[1]), true
}

// send mails one message (best effort: the statement is on the Broadcast page either way).
func (s *Store) send(ctx context.Context, to, kind string, params map[string]any) {
	if s.Mailer == nil || strings.TrimSpace(to) == "" {
		return
	}
	body, ok := MailText(kind, params)
	if !ok {
		return
	}
	subject := "Maktaba Cloud: copyright · سحابة مكتبة: حقوق النشر"
	if err := s.Mailer.Send(ctx, mail.Message{To: to, Subject: subject, Body: body, Kind: "moderation_" + kind}); err != nil {
		s.log().Warn("moderation: a mail was not sent", "err", err, "kind", kind)
	}
}

// mailPublisher mails the publisher's account and stamps the newest statement of that kind.
func (s *Store) mailPublisher(ctx context.Context, publisherID, kind string, params map[string]any) {
	if publisherID == "" {
		return
	}
	var to string
	if err := s.DB.QueryRowContext(ctx, `SELECT email FROM users WHERE id::text = $1`, publisherID).Scan(&to); err != nil {
		return
	}
	s.send(ctx, to, kind, params)
	_, _ = s.DB.ExecContext(ctx, `UPDATE moderation_statements SET emailed_at = now() WHERE id = (
        SELECT id FROM moderation_statements WHERE publisher_id::text = $1 AND kind = $2 AND emailed_at IS NULL
        ORDER BY created_at DESC LIMIT 1)`, publisherID, kind)
}

// Record is one row of the export: what it is, its id, when, and the row itself.
type Record struct {
	Table string         `json:"table"`
	ID    string         `json:"id"`
	At    time.Time      `json:"at"`
	Row   map[string]any `json:"row"`
}

// exportTables are the records a lawyer gets, each with its id and time columns.
var exportTables = []struct{ table, id, at string }{
	{"moderation_notices", "id", "received_at"},
	{"moderation_reports", "id", "received_at"},
	{"moderation_actions", "id", "created_at"},
	{"moderation_statements", "id", "created_at"},
	{"moderation_counter_notices", "id", "filed_at"},
	{"moderation_strikes", "id", "at"},
	{"moderation_terminations", "id", "at"},
	{"moderation_notifier_blocks", "email", "blocked_at"},
	{"moderation_audit", "id", "at"},
}

// Records is every record kept, oldest first per table (§5).
func (s *Store) Records(ctx context.Context) ([]Record, error) {
	out := []Record{}
	for _, t := range exportTables {
		rs, err := s.DB.QueryContext(ctx, `SELECT `+t.id+`::text, `+t.at+`, to_jsonb(x) FROM `+t.table+` x ORDER BY `+t.at)
		if err != nil {
			return nil, err
		}
		for rs.Next() {
			var r Record
			var raw []byte
			if err := rs.Scan(&r.ID, &r.At, &raw); err != nil {
				_ = rs.Close()
				return nil, err
			}
			if err := json.Unmarshal(raw, &r.Row); err != nil {
				_ = rs.Close()
				return nil, err
			}
			r.Table = t.table
			out = append(out, r)
		}
		if err := rs.Close(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// WriteCSV writes the records as CSV: table, id, at, and the row as JSON.
func WriteCSV(w io.Writer, records []Record) error {
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"table", "id", "at", "row"}); err != nil {
		return err
	}
	for _, r := range records {
		raw, err := json.Marshal(r.Row)
		if err != nil {
			return err
		}
		if err := cw.Write([]string{r.Table, r.ID, r.At.UTC().Format(time.RFC3339), string(raw)}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
