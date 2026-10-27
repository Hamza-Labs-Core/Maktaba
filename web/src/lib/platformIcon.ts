// The icon beside a device's platform: the admin's device list (pages/Cloud/Devices)
// and every user's own (pages/Profile/ConnectedDevices). Kept out of the admin page
// so a player build (Epic 42.5) carries none of it.
export function platformIcon(platform: string): string {
  switch (platform) {
    case "ios":
      return "";
    case "android":
      return "🤖";
    case "web":
      return "🌐";
    default:
      return "📱";
  }
}
