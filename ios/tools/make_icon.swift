#!/usr/bin/env swift
// Placeholder app icon: a winding road on the accent green. Opaque (the App Store rejects alpha).
// Run: swift tools/make_icon.swift Serpentine/Assets.xcassets/AppIcon.appiconset/AppIcon-1024.png

import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

let size = 1024
let out = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "AppIcon-1024.png"

let ctx = CGContext(data: nil, width: size, height: size, bitsPerComponent: 8, bytesPerRow: 0,
                    space: CGColorSpace(name: CGColorSpace.sRGB)!,
                    bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue)!
let s = CGFloat(size)

// Background: accent green, slightly lighter towards the top.
let bg = CGGradient(colorsSpace: CGColorSpace(name: CGColorSpace.sRGB)!,
                    colors: [CGColor(srgbRed: 0.06, green: 0.50, blue: 0.42, alpha: 1),
                             CGColor(srgbRed: 0.03, green: 0.33, blue: 0.28, alpha: 1)] as CFArray,
                    locations: [0, 1])!
ctx.drawLinearGradient(bg, start: CGPoint(x: 0, y: s), end: CGPoint(x: 0, y: 0), options: [])

// The road: an S-bend from bottom-left to top-right (CoreGraphics y is up).
let road = CGMutablePath()
road.move(to: CGPoint(x: s * 0.30, y: -s * 0.05))
road.addCurve(to: CGPoint(x: s * 0.50, y: s * 0.50),
              control1: CGPoint(x: s * 0.30, y: s * 0.25), control2: CGPoint(x: s * 0.85, y: s * 0.25))
road.addCurve(to: CGPoint(x: s * 0.70, y: s * 1.05),
              control1: CGPoint(x: s * 0.15, y: s * 0.75), control2: CGPoint(x: s * 0.70, y: s * 0.75))

ctx.setLineCap(.round)
ctx.addPath(road)
ctx.setStrokeColor(CGColor(srgbRed: 1, green: 1, blue: 1, alpha: 1))
ctx.setLineWidth(s * 0.17)
ctx.strokePath()

// Centre line.
ctx.addPath(road)
ctx.setStrokeColor(CGColor(srgbRed: 0.03, green: 0.33, blue: 0.28, alpha: 1))
ctx.setLineWidth(s * 0.018)
ctx.setLineDash(phase: 0, lengths: [s * 0.05, s * 0.04])
ctx.strokePath()

let image = ctx.makeImage()!
let dest = CGImageDestinationCreateWithURL(URL(fileURLWithPath: out) as CFURL, UTType.png.identifier as CFString, 1, nil)!
CGImageDestinationAddImage(dest, image, nil)
guard CGImageDestinationFinalize(dest) else { fatalError("could not write \(out)") }
print("wrote \(out)")
