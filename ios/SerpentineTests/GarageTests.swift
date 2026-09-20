import Foundation
import Testing
@testable import Serpentine

private func catalogEntry(
    id: String = "test", make: String = "Zero", model: String = "SR/S",
    nominal: Double? = 15.1, max: Double? = 17.3, city: Double? = 171,
    highway: Double? = 116, highwayLow: Double? = nil, ac: Double? = 6.6, dc: Double? = 0,
    connectors: [String]? = ["J1772"]
) -> CatalogVehicle {
    let json: [String: Any?] = [
        "id": id, "make": make, "model": model, "year": 2025, "kind": "electric",
        "battery_max_kwh": max, "battery_nominal_kwh": nominal,
        "range_city_mi": city, "range_highway_mi": highway, "range_highway_low_mi": highwayLow,
        "ac_kw": ac, "dc_kw": dc, "connectors": connectors,
        "confidence": "verified", "notes": nil,
    ]
    let data = try! JSONSerialization.data(withJSONObject: json.compactMapValues { $0 })
    return try! SerpentineAPI.decoder().decode(CatalogVehicle.self, from: data)
}

@Test func catalogueEntryBecomesARideableBike() throws {
    let bike = try #require(Bike(from: catalogEntry()))
    #expect(bike.name == "Zero SR/S")
    #expect(bike.usableKWh == 15.1, "the nominal pack, not the headline 17.3")
    // Round-tripping the published range through Wh/km must land back where it started.
    #expect(bike.cityRangeMiles == 171)
    #expect(bike.highwayRangeMiles == 116)
    #expect(!bike.estimated)
}

@Test func missingFiguresAreDerivedAndFlagged() throws {
    // Can-Am publishes neither a usable capacity nor a real highway figure.
    let bike = try #require(Bike(from: catalogEntry(
        make: "Can-Am", model: "Pulse", nominal: nil, max: 8.9, city: 100,
        highway: nil, highwayLow: 55)))
    #expect(bike.usableKWh == 8.9, "gross capacity is the only number published")
    #expect(bike.estimated, "a rider must be told which numbers we guessed")
    #expect(bike.highwayRangeMiles < bike.cityRangeMiles)
}

@Test func aBikeWithNoUsableFiguresIsRejected() {
    #expect(Bike(from: catalogEntry(nominal: nil, max: nil)) == nil)
    #expect(Bike(from: catalogEntry(city: nil)) == nil)
}

@Test func adaptersWidenTheSearchButNotTheChargeRate() {
    var bike = Bike.srs
    bike.adapters = ["TESLA"]
    #expect(bike.wire.connectors == ["J1772", "TESLA"])
    #expect(bike.wire.acKw == 6.6, "an adapter doesn't make the onboard charger faster")
}

@Test func aDCOnlyBikeIsCaughtBeforePlanning() {
    // The LiveWire ONE: the server refuses this, and the app shouldn't let it get that far.
    let one = Bike(name: "LiveWire ONE", usableKWh: 15.4, cityWhPerKM: 65, highwayWhPerKM: 100,
                   acKW: 1.4, dcKW: 13, connectors: ["J1772COMBO"])
    #expect(one.problem != nil)
    #expect(Bike.srs.problem == nil)
}

@MainActor @Test func garagePersistsAndNeverEmpties() throws {
    let defaults = try #require(UserDefaults(suiteName: "garage-test-\(UUID().uuidString)"))
    let garage = Garage(defaults: defaults)
    #expect(garage.bikes.count == 1, "a fresh garage holds the default bike")

    let pulse = Bike(name: "Can-Am Pulse", usableKWh: 8.9, cityWhPerKM: 55, highwayWhPerKM: 69,
                     acKW: 6.6, dcKW: 0, connectors: ["J1772"])
    garage.add(pulse)
    #expect(garage.selected.id == pulse.id, "adding a bike selects it")

    // A second garage on the same defaults is what relaunching the app looks like.
    let reopened = Garage(defaults: defaults)
    #expect(reopened.bikes.count == 2)
    #expect(reopened.selected.name == "Can-Am Pulse")

    reopened.remove(reopened.selected)
    #expect(reopened.bikes.count == 1)
    reopened.remove(reopened.selected)
    #expect(reopened.bikes.count == 1, "the last bike can't be removed")
}

@MainActor @Test func theBikeTravelsOnlyWithCharging() throws {
    let p = Planner()
    p.start = StartPoint(name: "Ramona", coordinate: [-116.868, 33.042].coordinate)
    p.bike = Bike(name: "Can-Am Pulse", usableKWh: 8.9, cityWhPerKM: 55, highwayWhPerKM: 69,
                  acKW: 6.6, dcKW: 0, connectors: ["J1772"])
    #expect(p.request(seed: 1)?.vehicle == nil, "no charging, nothing to model")
    p.charging = true
    let req = try #require(p.request(seed: 1))
    let vehicle = try #require(req.vehicle)
    #expect(vehicle.usableKwh == 8.9 && vehicle.name == "Can-Am Pulse")
}
