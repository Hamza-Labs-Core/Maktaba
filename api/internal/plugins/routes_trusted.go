package plugins

// The trusted routes (Epic 54, story 54.24 §2): Maktaba's own first-party plugins reach the
// database through these alone, by the names of the statements the release built.
//
//	trusted/stmt   {name, op: fetch|execute|executemany, args, rows, tx, limit}
//	trusted/tx     {op: begin|commit|rollback|savepoint|release|rollback_to, tx, sp}
//
// ServeHTTP lets only a trusted invocation (a first-party token) reach them.

import (
	"log/slog"

	"github.com/Hamza-Labs-Core/Maktaba/api/internal/plugins/statements"
)

// executor answers the server's statements executor, registering the embedded
// first-party plugins' statements the first time.
func (s *Server) executor() *statements.Executor {
	s.stmtOnce.Do(func() {
		if s.Statements == nil {
			s.Statements = statements.New(s.DB)
		}
		list, err := FirstPartyPlugins()
		if err != nil {
			s.logger().Error("plugin_firstparty_invalid", "err", err)
			return
		}
		for _, fp := range list {
			if err := s.Statements.Register(fp.ID, fp.Statements); err != nil {
				s.logger().Error("plugin_firstparty_statements_invalid", "plugin", fp.ID, "err", err)
			}
		}
		prev := s.Tokens.OnEnd
		s.Tokens.OnEnd = func(id string) {
			s.Statements.EndInvocation(id)
			if prev != nil {
				prev(id)
			}
		}
	})
	return s.Statements
}

func scopeOf(inv *Invocation) statements.Scope {
	sc := statements.Scope{Invocation: inv.ID, PluginID: inv.PluginID, Version: inv.Version,
		SubjectKind: inv.SubjectKind, SubjectID: inv.SubjectID, LibraryID: inv.LibraryID, ReadOnly: inv.ReadOnly}
	if sc.SubjectKind == "library" && sc.LibraryID == "" {
		sc.LibraryID = inv.SubjectID
	}
	if inv.FirstParty != nil {
		sc.Settings = inv.FirstParty.Trusted.Settings
	}
	return sc
}

func fromStmtErr(e *statements.Error) *Refusal {
	return &Refusal{Status: e.Status, Code: e.Code, Detail: e.Detail}
}

func (s *Server) trustedRoutes() {
	s.routes["trusted/stmt"] = func(c *call) (any, *Refusal) {
		var req statements.Request
		if ref := c.decode(&req); ref != nil {
			return nil, ref
		}
		out, err := s.executor().Stmt(c.ctx, scopeOf(c.inv), req)
		if err != nil {
			if err.Code == statements.CodeDatabase {
				s.logger().Debug("plugin_statement_failed", slog.String("plugin", c.inv.PluginID),
					slog.String("statement", req.Name), slog.String("code", err.Detail))
			}
			return nil, fromStmtErr(err)
		}
		return out, nil
	}
	s.routes["trusted/tx"] = func(c *call) (any, *Refusal) {
		var req statements.TxRequest
		if ref := c.decode(&req); ref != nil {
			return nil, ref
		}
		out, err := s.executor().Tx(c.ctx, scopeOf(c.inv), req)
		if err != nil {
			return nil, fromStmtErr(err)
		}
		return out, nil
	}
}
