# Branch protection for `main` (Story 22.1, AC2 + TC3).
#
# `ci-success` is the single required check. When a new gate is added
# in `.github/workflows/ci.yml`, wire it into `ci-success.needs:` —
# this file does NOT need to change. That's the whole point of the
# rollup pattern: branch protection has one stable contract.
#
# Force-merge audit trail lives in PR bodies, validated by
# `.github/workflows/_pr-body-check.yml` (gated by the `force-merge`
# label). There is no Terraform-level bypass.

resource "github_branch_protection" "main" {
  repository_id = data.github_repository.maktaba.node_id
  pattern       = "main"

  required_status_checks {
    strict   = true
    contexts = ["ci-success"]
  }

  required_pull_request_reviews {
    required_approving_review_count = 1
    dismiss_stale_reviews           = true
    require_code_owner_reviews      = true
  }

  enforce_admins         = true
  require_signed_commits = true
  allows_force_pushes    = false
  allows_deletions       = false

  # Conversation-resolution required so reviewer comments aren't
  # silently dismissed.
  require_conversation_resolution = true
}
