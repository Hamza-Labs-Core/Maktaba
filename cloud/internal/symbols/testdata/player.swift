struct Player { func play(item: Int) -> Int { let a: [Int] = []; return a[item] } }
@main struct App { static func main() { print(Player().play(item: CommandLine.arguments.count)) } }
