import Foundation
import Observation

/// A bike the rider owns. Catalogue entries and hand-typed ones are the same type on purpose: the
/// server can't tell them apart either (ADR-025), so "the spec sheet is wrong for mine" is an
/// ordinary edit rather than a special case.
struct Bike: Codable, Identifiable, Equatable, Sendable {
    var id = UUID()
    var name: String
    var usableKWh: Double
    var cityWhPerKM: Double
    var highwayWhPerKM: Double
    var acKW: Double
    var dcKW: Double
    /// What the bike's own socket takes, in NREL's vocabulary: J1772, J1772COMBO, CHADEMO, TESLA.
    var connectors: [String]
    /// What the rider carries in the top box. Adapters belong to the person, not the machine
    /// (ADR-020), so they're stored beside the bike and added to what we search for.
    var adapters: [String] = []
    /// True when the catalogue didn't publish something and the app had to derive it — shown in the
    /// UI so a rider knows which numbers deserve a second look.
    var estimated = false
    var catalogID: String?

    /// What goes on the wire. Adapters widen the search; the bike alone decides how fast it charges.
    var wire: VehicleWire {
        VehicleWire(name: name, kind: "electric", usableKwh: usableKWh,
                    cityWhPerKm: cityWhPerKM.rounded(), highwayWhPerKm: highwayWhPerKM.rounded(),
                    acKw: acKW, dcKw: dcKW,
                    connectors: Array(Set(connectors + adapters)).sorted())
    }

    /// Miles of range the numbers imply, for display. Not a promise — the server's model is
    /// pessimistic on top of this.
    var cityRangeMiles: Int { Int((usableKWh / (cityWhPerKM / 1000) / 1.609344).rounded()) }
    var highwayRangeMiles: Int { Int((usableKWh / (highwayWhPerKM / 1000) / 1.609344).rounded()) }

    static let srs = Bike(name: "Zero SR/S", usableKWh: 15.1,
                          cityWhPerKM: 15.1 / (171 * 1.609344) * 1000,
                          highwayWhPerKM: 15.1 / (116 * 1.609344) * 1000,
                          acKW: 6.6, dcKW: 0, connectors: ["J1772"], adapters: ["TESLA"],
                          catalogID: "zero-srs")

    /// Builds a bike from a catalogue entry, deriving what the manufacturer didn't publish.
    /// Returns nil when there isn't enough to model at all.
    init?(from v: CatalogVehicle) {
        guard v.kind == "electric" else { return nil }
        guard let pack = v.batteryNominalKwh ?? v.batteryMaxKwh, pack > 0 else { return nil }
        guard let city = v.rangeCityMi, city > 0 else { return nil }

        // Highway: the published figure, else the slower "low-speed highway" one, else a cautious
        // guess. Manufacturers stopped publishing the speeds behind these (ADR-020), so anything
        // derived is flagged rather than presented as fact.
        var derived = v.batteryNominalKwh == nil
        let highway: Double
        if let h = v.rangeHighwayMi, h > 0 {
            highway = h
        } else if let low = v.rangeHighwayLowMi, low > 0 {
            highway = low * 0.8
            derived = true
        } else {
            highway = city * 0.6
            derived = true
        }
        // Round once, here: the same kW shown two ways ("0.6" in one place, "0.7" in another) reads
        // like a bug even when both are the same number.
        let ac = ((v.acKw ?? 0) * 10).rounded() / 10
        self.init(name: "\(v.make) \(v.model)",
                  usableKWh: pack,
                  cityWhPerKM: pack / (city * 1.609344) * 1000,
                  highwayWhPerKM: pack / (highway * 1.609344) * 1000,
                  acKW: ac, dcKW: v.dcKw ?? 0,
                  connectors: v.connectors ?? ["J1772"],
                  adapters: [], estimated: derived, catalogID: v.id)
    }

    init(id: UUID = UUID(), name: String, usableKWh: Double, cityWhPerKM: Double, highwayWhPerKM: Double,
         acKW: Double, dcKW: Double, connectors: [String], adapters: [String] = [],
         estimated: Bool = false, catalogID: String? = nil) {
        self.id = id
        self.name = name
        self.usableKWh = usableKWh
        self.cityWhPerKM = cityWhPerKM
        self.highwayWhPerKM = highwayWhPerKM
        self.acKW = acKW
        self.dcKW = dcKW
        self.connectors = connectors
        self.adapters = adapters
        self.estimated = estimated
        self.catalogID = catalogID
    }

    /// Mirrors the server's validation (ADR-025), so a bad bike is caught before a ride is planned.
    var problem: String? {
        if name.trimmingCharacters(in: .whitespaces).isEmpty { return "Give the bike a name." }
        if usableKWh <= 0 || usableKWh > 60 { return "Battery should be between 0 and 60 kWh." }
        if cityWhPerKM <= 0 || highwayWhPerKM <= 0 { return "Range figures must be above zero." }
        if acKW < 2 && !(connectors + adapters).contains(where: { $0 != "J1772COMBO" && $0 != "CHADEMO" }) {
            return "This bike only fast-charges, and DC stops aren't planned yet."
        }
        return nil
    }
}

/// The bikes on this phone and which one is being ridden. Local only: the server never learns who
/// owns what, it just gets told what to plan for.
@MainActor @Observable
final class Garage {
    private(set) var bikes: [Bike]
    var selectedID: UUID {
        didSet { save() }
    }
    private(set) var catalog: [CatalogVehicle] = []
    private(set) var catalogError: String?

    private let defaults: UserDefaults
    private let api: SerpentineAPI
    private static let bikesKey = "garage.bikes"
    private static let selectedKey = "garage.selected"

    init(defaults: UserDefaults = .standard, api: SerpentineAPI = .production) {
        self.defaults = defaults
        self.api = api
        let stored = (try? JSONDecoder().decode([Bike].self, from: defaults.data(forKey: Self.bikesKey) ?? Data())) ?? []
        let loaded = stored.isEmpty ? [Bike.srs] : stored
        let saved = defaults.string(forKey: Self.selectedKey).flatMap(UUID.init(uuidString:))
        bikes = loaded
        selectedID = loaded.contains(where: { $0.id == saved }) ? saved! : loaded[0].id
    }

    var selected: Bike { bikes.first { $0.id == selectedID } ?? bikes[0] }

    func add(_ bike: Bike) {
        bikes.append(bike)
        selectedID = bike.id // selecting it is what the rider meant by adding it
        save()
    }

    func update(_ bike: Bike) {
        guard let i = bikes.firstIndex(where: { $0.id == bike.id }) else { return }
        bikes[i] = bike
        save()
    }

    func remove(_ bike: Bike) {
        guard bikes.count > 1 else { return } // never leave the garage empty
        bikes.removeAll { $0.id == bike.id }
        if selectedID == bike.id { selectedID = bikes[0].id }
        save()
    }

    /// The catalogue is a convenience, cached by the app; a plan never depends on it being reachable.
    func loadCatalog() async {
        guard catalog.isEmpty else { return }
        do {
            catalog = try await api.vehicles()
            catalogError = nil
        } catch {
            catalogError = error.localizedDescription
        }
    }

    private func save() {
        defaults.set(try? JSONEncoder().encode(bikes), forKey: Self.bikesKey)
        defaults.set(selectedID.uuidString, forKey: Self.selectedKey)
    }
}
