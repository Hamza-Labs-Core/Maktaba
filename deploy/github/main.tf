# GitHub provider for branch-protection IaC (Story 22.1).
#
# State is stored in the team's existing Terraform backend; this file
# only declares the provider so `branch-protection.tf` stays focused.

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    github = {
      source  = "integrations/github"
      version = "~> 6.3"
    }
  }
}

provider "github" {
  # Auth via GITHUB_TOKEN env var (a fine-grained PAT with repo:admin
  # scope on Hamza-Labs-Core/Maktaba). Owner is set via env var
  # GITHUB_OWNER so the same module can target a fork during dry-runs.
}

data "github_repository" "maktaba" {
  full_name = "Hamza-Labs-Core/Maktaba"
}
