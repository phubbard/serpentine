import Foundation

/// Display formatting. Distances follow the rider's locale for roads (miles in the US).
enum Format {
    static func distance(km: Double) -> String {
        Measurement(value: km, unit: UnitLength.kilometers)
            .formatted(.measurement(width: .abbreviated, usage: .road, numberFormatStyle: .number.precision(.fractionLength(0))))
    }

    /// Distance to a search result. Whole miles are useless when the shop is round the corner, so
    /// anything under ten keeps a decimal. The unit is pinned rather than left to `usage: .road`,
    /// which turns half a mile into "2,099.7 ft".
    static func nearby(meters: Double, locale: Locale = .current) -> String {
        let unit: UnitLength = locale.measurementSystem == .metric ? .kilometers : .miles
        var value = Measurement(value: meters, unit: UnitLength.meters).converted(to: unit)
        // "0.0 mi" reads as a bug rather than as "you're standing on it".
        var prefix = ""
        if value.value < 0.1 {
            value = Measurement(value: 0.1, unit: unit)
            prefix = "< "
        }
        return prefix + value.formatted(.measurement(width: .abbreviated, usage: .asProvided,
                                            numberFormatStyle: .number.precision(.fractionLength(value.value < 10 ? 1 : 0)))
            .locale(locale))
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

    /// A share of the battery, for the reach list. "0 %" for a ride that costs almost nothing reads
    /// as a broken estimate rather than as "that one's free", so say "<1 %" instead.
    static func packShare(_ fraction: Double) -> String {
        if fraction > 0 && fraction < 0.01 { return "<1 %" }
        return percent(fraction)
    }

    static func percent(_ fraction: Double) -> String {
        "\(Int((fraction * 100).rounded())) %"
    }
}
