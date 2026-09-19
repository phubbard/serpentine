import Foundation

/// Display formatting. Distances follow the rider's locale for roads (miles in the US).
enum Format {
    static func distance(km: Double) -> String {
        Measurement(value: km, unit: UnitLength.kilometers)
            .formatted(.measurement(width: .abbreviated, usage: .road, numberFormatStyle: .number.precision(.fractionLength(0))))
    }

    static func duration(seconds: Double) -> String {
        let minutes = Int((seconds / 60).rounded())
        return minutes < 60 ? "\(minutes) min" : "\(minutes / 60) h \(String(format: "%02d", minutes % 60)) min"
    }

    /// Elevation gain: feet in the US, metres everywhere else (the UK too, whose `.uk` measurement
    /// system still means miles for roads). `usage: .road` would turn 2,400 m of climbing into "2 mi".
    static func climb(meters: Double, locale: Locale = .current) -> String {
        let unit: UnitLength = locale.measurementSystem == .us ? .feet : .meters
        return Measurement(value: meters, unit: UnitLength.meters).converted(to: unit)
            .formatted(.measurement(width: .abbreviated, usage: .asProvided,
                                    numberFormatStyle: .number.precision(.fractionLength(0)))
                .locale(locale))
    }

    static func percent(_ fraction: Double) -> String {
        "\(Int((fraction * 100).rounded())) %"
    }
}
