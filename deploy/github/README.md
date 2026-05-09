# `deploy/github`

Terraform that owns repo-level GitHub config (branch protection, etc.).
Story 22.1 created it; future devops stories add more resources here.

## Apply

```sh
export GITHUB_TOKEN=<fine-grained PAT, repo:admin scope>
export GITHUB_OWNER=Hamza-Labs-Core
terraform init
terraform plan
terraform apply
```

State lives in the team's existing Terraform backend (configure
locally before applying). Never commit `*.tfstate*` or `.terraform/`.
