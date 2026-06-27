#!/usr/bin/env swift

import AppKit
import ApplicationServices
import Foundation
import Vision

struct RunningApps: Decodable {
  let apps: [RunningApp]
}

struct RunningApp: Decodable {
  let bundleID: String
  let name: String
  let pid: Int
  let running: Bool

  enum CodingKeys: String, CodingKey {
    case bundleID = "bundle_id"
    case name
    case pid
    case running
  }
}

struct WindowList: Decodable {
  let windows: [AppWindow]
}

struct AppWindow: Decodable {
  let windowID: Int
  let title: String
  let isOnScreen: Bool
  let onCurrentSpace: Bool
  let bounds: WindowBounds

  enum CodingKeys: String, CodingKey {
    case windowID = "window_id"
    case title
    case isOnScreen = "is_on_screen"
    case onCurrentSpace = "on_current_space"
    case bounds
  }
}

struct WindowBounds: Decodable {
  let width: Double
  let height: Double
}

struct RecognizedText {
  let text: String
  let rect: CGRect
}

struct Config {
  var targetTitle = "rs bilete"
  var codes: [String] = []
  var evidenceDir = "ops/evidence/rigassatiksme-qr-bot/telegram-target-locked-stress"
  var selectChat = false
  var dryRun = false
  var resizeIfNeeded = false
  var pauseMillis = 1500
}

enum StressError: Error, CustomStringConvertible {
  case usage(String)
  case commandFailed(String)
  case targetNotFound(String)
  case invalidCode(String)

  var description: String {
    switch self {
    case .usage(let message), .commandFailed(let message), .targetNotFound(let message), .invalidCode(let message):
      return message
    }
  }
}

func runProcess(_ arguments: [String]) throws -> String {
  let process = Process()
  process.executableURL = URL(fileURLWithPath: "/usr/bin/env")
  process.arguments = arguments
  let stdout = Pipe()
  let stderr = Pipe()
  process.standardOutput = stdout
  process.standardError = stderr
  try process.run()
  process.waitUntilExit()
  let out = String(data: stdout.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
  let err = String(data: stderr.fileHandleForReading.readDataToEndOfFile(), encoding: .utf8) ?? ""
  if process.terminationStatus != 0 {
    throw StressError.commandFailed((err.isEmpty ? out : err).trimmingCharacters(in: .whitespacesAndNewlines))
  }
  return out
}

func cua(_ tool: String, _ payload: [String: Any], extra: [String] = []) throws -> String {
  let data = try JSONSerialization.data(withJSONObject: payload, options: [.sortedKeys])
  let json = String(data: data, encoding: .utf8) ?? "{}"
  return try runProcess(["cua-driver", tool, json] + extra)
}

func parseArgs() throws -> Config {
  var config = Config()
  var args = Array(CommandLine.arguments.dropFirst())
  while !args.isEmpty {
    let arg = args.removeFirst()
    switch arg {
    case "--target":
      guard !args.isEmpty else { throw StressError.usage("--target requires a value") }
      config.targetTitle = args.removeFirst()
    case "--codes":
      guard !args.isEmpty else { throw StressError.usage("--codes requires a comma-separated value") }
      config.codes = args.removeFirst().split(separator: ",").map { String($0).trimmingCharacters(in: .whitespacesAndNewlines) }
    case "--evidence-dir":
      guard !args.isEmpty else { throw StressError.usage("--evidence-dir requires a value") }
      config.evidenceDir = args.removeFirst()
    case "--pause-ms":
      guard !args.isEmpty, let value = Int(args.removeFirst()) else { throw StressError.usage("--pause-ms requires an integer") }
      config.pauseMillis = value
    case "--select":
      config.selectChat = true
    case "--resize-if-needed":
      config.resizeIfNeeded = true
    case "--dry-run":
      config.dryRun = true
    case "--help", "-h":
      throw StressError.usage("""
      Usage: telegram_target_locked_stress.swift --codes 68803,58011 --evidence-dir <dir> [--select] [--dry-run]

      The tool refuses to type unless OCR proves the selected Telegram chat header is the RS bot.
      """)
    default:
      throw StressError.usage("unknown argument: \(arg)")
    }
  }
  for code in config.codes where code.range(of: #"^[0-9]{5}$"#, options: .regularExpression) == nil {
    throw StressError.invalidCode("invalid code: \(code)")
  }
  if config.codes.isEmpty && !config.dryRun {
    throw StressError.usage("--codes is required unless --dry-run is set")
  }
  return config
}

func folded(_ value: String) -> String {
  value.folding(options: [.diacriticInsensitive, .caseInsensitive], locale: Locale(identifier: "lv_LV"))
    .lowercased()
    .replacingOccurrences(of: "\n", with: " ")
}

func telegramWindows(pid: Int) throws -> [AppWindow] {
  let windowsData = try cua("list_windows", ["pid": pid]).data(using: .utf8) ?? Data()
  return try JSONDecoder().decode(WindowList.self, from: windowsData).windows
}

func findTelegram(resizeIfNeeded: Bool) throws -> (Int, Int, CGSize) {
  let appsData = try runProcess(["cua-driver", "list_apps", "{}"]).data(using: .utf8) ?? Data()
  let apps = try JSONDecoder().decode(RunningApps.self, from: appsData)
  guard let app = apps.apps.first(where: { $0.bundleID == "ru.keepcoder.Telegram" && $0.running }) else {
    throw StressError.targetNotFound("Telegram is not running")
  }
  var windows = try telegramWindows(pid: app.pid)
  if windows.first(where: { $0.isOnScreen && $0.onCurrentSpace && $0.bounds.width >= 700 && $0.bounds.height >= 500 }) == nil &&
    resizeIfNeeded {
    try resizeTelegramMainWindow(pid: app.pid)
    sleepMillis(500)
    windows = try telegramWindows(pid: app.pid)
  }
  guard let window = windows.first(where: { $0.isOnScreen && $0.onCurrentSpace && $0.bounds.width >= 700 && $0.bounds.height >= 500 }) else {
    throw StressError.targetNotFound("No visible Telegram window found on the current Space")
  }
  return (app.pid, window.windowID, CGSize(width: window.bounds.width, height: window.bounds.height))
}

func resizeTelegramMainWindow(pid: Int) throws {
  let app = AXUIElementCreateApplication(pid_t(pid))
  var windowsRef: CFTypeRef?
  let copyError = AXUIElementCopyAttributeValue(app, kAXWindowsAttribute as CFString, &windowsRef)
  guard copyError == .success, let windows = windowsRef as? [AXUIElement], let window = windows.first else {
    throw StressError.commandFailed("could not read Telegram accessibility windows: \(copyError.rawValue)")
  }
  var position = CGPoint(x: 120, y: 80)
  var size = CGSize(width: 900, height: 850)
  guard let positionValue = AXValueCreate(.cgPoint, &position),
        let sizeValue = AXValueCreate(.cgSize, &size) else {
    throw StressError.commandFailed("could not create accessibility window frame values")
  }
  let positionError = AXUIElementSetAttributeValue(window, kAXPositionAttribute as CFString, positionValue)
  let sizeError = AXUIElementSetAttributeValue(window, kAXSizeAttribute as CFString, sizeValue)
  guard positionError == .success && sizeError == .success else {
    throw StressError.commandFailed("could not resize Telegram window: position=\(positionError.rawValue) size=\(sizeError.rawValue)")
  }
}

func screenshot(pid: Int, windowID: Int, path: String) throws {
  _ = try cua("screenshot", ["pid": pid, "window_id": windowID], extra: ["--screenshot-out-file", path])
}

func recognize(path: String, size: CGSize) throws -> [RecognizedText] {
  guard let image = NSImage(contentsOfFile: path),
        let cgImage = image.cgImage(forProposedRect: nil, context: nil, hints: nil) else {
    throw StressError.commandFailed("could not load screenshot: \(path)")
  }
  let request = VNRecognizeTextRequest()
  request.recognitionLevel = .accurate
  request.usesLanguageCorrection = false
  request.recognitionLanguages = ["en-US", "lv-LV", "ru-RU"]
  let handler = VNImageRequestHandler(cgImage: cgImage, options: [:])
  try handler.perform([request])
  return (request.results ?? []).compactMap { observation in
    guard let candidate = observation.topCandidates(1).first else { return nil }
    let box = observation.boundingBox
    let rect = CGRect(
      x: box.minX * size.width,
      y: (1.0 - box.maxY) * size.height,
      width: box.width * size.width,
      height: box.height * size.height
    )
    return RecognizedText(text: candidate.string, rect: rect)
  }
}

func targetIsSelected(_ texts: [RecognizedText], target: String, size: CGSize) -> Bool {
  let needle = folded(target)
  return texts.contains { item in
    item.rect.minX >= size.width * 0.36 &&
      item.rect.maxY <= size.height * 0.24 &&
      folded(item.text).contains(needle)
  }
}

func findTargetInChatList(_ texts: [RecognizedText], target: String, size: CGSize) -> CGPoint? {
  let needle = folded(target)
  let candidates = texts.filter { item in
    item.rect.maxX <= size.width * 0.38 &&
      item.rect.minY >= size.height * 0.06 &&
      item.rect.minY <= size.height * 0.90 &&
      folded(item.text).contains(needle)
  }
  guard let best = candidates.min(by: { $0.rect.minY < $1.rect.minY }) else {
    return nil
  }
  return CGPoint(x: best.rect.midX, y: best.rect.midY)
}

func writeProof(_ path: String, texts: [RecognizedText], selected: Bool) throws {
  let lines = texts.map { item in
    "\(Int(item.rect.minX)),\(Int(item.rect.minY)),\(Int(item.rect.width)),\(Int(item.rect.height)) \(item.text)"
  }.joined(separator: "\n")
  let body = "selected=\(selected)\n\(lines)\n"
  try body.write(toFile: path, atomically: true, encoding: .utf8)
}

func verifySelected(pid: Int, windowID: Int, size: CGSize, config: Config, evidenceName: String) throws -> [RecognizedText] {
  let shot = "\(config.evidenceDir)/\(evidenceName).png"
  try screenshot(pid: pid, windowID: windowID, path: shot)
  let texts = try recognize(path: shot, size: size)
  let selected = targetIsSelected(texts, target: config.targetTitle, size: size)
  try writeProof("\(config.evidenceDir)/\(evidenceName)-ocr.txt", texts: texts, selected: selected)
  guard selected else {
    throw StressError.targetNotFound("Telegram selected-chat header is not \(config.targetTitle); refusing to type")
  }
  return texts
}

func sleepMillis(_ millis: Int) {
  usleep(useconds_t(max(0, millis) * 1000))
}

do {
  let config = try parseArgs()
  try FileManager.default.createDirectory(atPath: config.evidenceDir, withIntermediateDirectories: true)
  let (pid, windowID, size) = try findTelegram(resizeIfNeeded: config.resizeIfNeeded)

  do {
    _ = try verifySelected(pid: pid, windowID: windowID, size: size, config: config, evidenceName: "telegram-target-initial")
  } catch {
    guard config.selectChat else { throw error }
    let initialShot = "\(config.evidenceDir)/telegram-select-search.png"
    try screenshot(pid: pid, windowID: windowID, path: initialShot)
    let texts = try recognize(path: initialShot, size: size)
    guard let point = findTargetInChatList(texts, target: config.targetTitle, size: size) else {
      try writeProof("\(config.evidenceDir)/telegram-select-search-ocr.txt", texts: texts, selected: false)
      throw StressError.targetNotFound("Could not find \(config.targetTitle) in the Telegram chat list")
    }
    _ = try cua("click", ["pid": pid, "window_id": windowID, "x": Int(point.x), "y": Int(point.y)])
    sleepMillis(700)
    _ = try verifySelected(pid: pid, windowID: windowID, size: size, config: config, evidenceName: "telegram-target-after-select")
  }

  if config.dryRun {
    print("target verified; dry run complete")
    exit(0)
  }

  for (index, code) in config.codes.enumerated() {
    _ = try verifySelected(
      pid: pid,
      windowID: windowID,
      size: size,
      config: config,
      evidenceName: String(format: "telegram-before-send-%02d", index + 1)
    )
    let inputX = Int(size.width * 0.72)
    let inputY = Int(size.height * 0.955)
    _ = try cua("click", ["pid": pid, "window_id": windowID, "x": inputX, "y": inputY])
    _ = try cua("type_text", ["pid": pid, "text": code])
    _ = try cua("press_key", ["pid": pid, "key": "ENTER"])
    sleepMillis(config.pauseMillis)
  }

  try screenshot(pid: pid, windowID: windowID, path: "\(config.evidenceDir)/telegram-after-send.png")
  print("sent \(config.codes.count) target-locked code(s) to \(config.targetTitle)")
} catch {
  fputs("telegram target-locked stress failed: \(error)\n", stderr)
  exit(1)
}
