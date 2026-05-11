# Terraform module for a single-VM Maktaba server on Hetzner Cloud
# (story 25.33). Operators apply this with their HCLOUD_TOKEN; the
# cloud-init writes the server.toml and pulls the Docker image.
terraform {
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.45"
    }
  }
}

variable "hcloud_token" {
  type      = string
  sensitive = true
}

provider "hcloud" {
  token = var.hcloud_token
}

resource "hcloud_server" "maktaba" {
  name        = "maktaba-server"
  image       = "ubuntu-24.04"
  server_type = "cx22"
  location    = "fsn1"
  user_data = templatefile("${path.module}/cloud-init.yaml", {
    image_tag = "stable"
  })
  labels = {
    app = "maktaba-server"
  }
}

output "server_ip" {
  value = hcloud_server.maktaba.ipv4_address
}
