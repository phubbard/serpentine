import SwiftUI

/// The bikes on this phone: pick one, add from the catalogue, or type your own numbers. Nothing here
/// leaves the device except as part of a plan request (ADR-025).
struct GarageView: View {
    @Environment(Garage.self) private var garage
    @Environment(\.dismiss) private var dismiss
    @State private var adding = false
    @State private var editing: Bike?

    var body: some View {
        @Bindable var garage = garage
        NavigationStack {
            List {
                Section {
                    ForEach(garage.bikes) { bike in
                        Button {
                            garage.selectedID = bike.id
                        } label: {
                            row(bike)
                        }
                        .buttonStyle(.plain)
                        .swipeActions {
                            if garage.bikes.count > 1 {
                                Button("Delete", role: .destructive) { garage.remove(bike) }
                            }
                            Button("Edit") { editing = bike }.tint(.accentColor)
                        }
                    }
                } footer: {
                    Text("Charge stops are planned for the bike you pick here. Swipe a bike to edit its numbers or remove it.")
                }

                Section {
                    Button("Add a bike", systemImage: "plus") { adding = true }
                }
            }
            .navigationTitle("Garage")
            .toolbar { Button("Done") { dismiss() } }
            .sheet(isPresented: $adding) { AddBikeView() }
            .sheet(item: $editing) { bike in BikeEditor(bike: bike) }
        }
    }

    private func row(_ bike: Bike) -> some View {
        HStack(alignment: .firstTextBaseline) {
            VStack(alignment: .leading, spacing: 2) {
                Text(bike.name).bold()
                Text("\(bike.usableKWh, specifier: "%.1f") kWh · \(bike.cityRangeMiles)–\(bike.highwayRangeMiles) mi · \(bike.acKW, specifier: "%.1f") kW")
                    .font(.subheadline).foregroundStyle(.secondary)
                if bike.estimated {
                    Text("Some figures estimated — check them against your bike.")
                        .font(.caption).foregroundStyle(.orange)
                }
            }
            Spacer()
            if bike.id == garage.selectedID {
                Image(systemName: "checkmark").foregroundStyle(Color.accentColor).bold()
            }
        }
        .contentShape(Rectangle())
    }
}

/// Pick from the catalogue the server publishes, or start from blank.
struct AddBikeView: View {
    @Environment(Garage.self) private var garage
    @Environment(\.dismiss) private var dismiss
    @State private var custom: Bike?

    var body: some View {
        NavigationStack {
            List {
                Section {
                    Button("Enter my own numbers", systemImage: "square.and.pencil") {
                        custom = Bike(name: "My bike", usableKWh: 10, cityWhPerKM: 55, highwayWhPerKM: 80,
                                      acKW: 6.6, dcKW: 0, connectors: ["J1772"])
                    }
                }
                Section("Catalogue") {
                    if let error = garage.catalogError {
                        Text("Couldn't load the catalogue: \(error)").foregroundStyle(.secondary)
                    }
                    ForEach(garage.catalog) { entry in
                        if let bike = Bike(from: entry) {
                            Button {
                                garage.add(bike)
                                dismiss()
                            } label: {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(bike.name).bold()
                                    Text("\(bike.usableKWh, specifier: "%.1f") kWh · \(bike.cityRangeMiles)–\(bike.highwayRangeMiles) mi")
                                        .font(.subheadline).foregroundStyle(.secondary)
                                    if let note = entry.notes, entry.confidence != "verified" {
                                        Text(note).font(.caption).foregroundStyle(.secondary).lineLimit(2)
                                    }
                                }
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }
            }
            .navigationTitle("Add a bike")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar { Button("Cancel") { dismiss() } }
            .task { await garage.loadCatalog() }
            .sheet(item: $custom) { bike in BikeEditor(bike: bike, isNew: true) }
        }
    }
}

/// Every number the model uses, editable. A rider who has measured their own consumption should be
/// able to say so — the spec sheet is only a starting point.
struct BikeEditor: View {
    @Environment(Garage.self) private var garage
    @Environment(\.dismiss) private var dismiss
    @State private var draft: Bike
    private let isNew: Bool

    init(bike: Bike, isNew: Bool = false) {
        _draft = State(initialValue: bike)
        self.isNew = isNew
    }

    private let allConnectors = ["J1772", "TESLA", "J1772COMBO", "CHADEMO"]
    private func label(_ c: String) -> String {
        switch c {
        case "J1772": "J1772 (Level 2)"
        case "TESLA": "Tesla destination"
        case "J1772COMBO": "CCS (Level 3)"
        default: "CHAdeMO (Level 3)"
        }
    }

    var body: some View {
        NavigationStack {
            Form {
                Section("Bike") {
                    TextField("Name", text: $draft.name)
                }
                Section {
                    stepperRow("Battery", value: $draft.usableKWh, step: 0.1, unit: "kWh", format: "%.1f")
                    stepperRow("Charges at", value: $draft.acKW, step: 0.1, unit: "kW", format: "%.1f")
                } header: {
                    Text("Battery and charging")
                } footer: {
                    Text("Usable capacity, not the headline number, and the bike's own charger rate — a public post rarely gives more.")
                }
                Section {
                    rangeRow("City range", wh: $draft.cityWhPerKM)
                    rangeRow("Highway range", wh: $draft.highwayWhPerKM)
                } header: {
                    Text("Range")
                } footer: {
                    Text("What you actually get, not the brochure. Serpentine is pessimistic on top of these.")
                }
                Section {
                    ForEach(allConnectors, id: \.self) { c in
                        Toggle(label(c), isOn: binding(for: c, in: \.connectors))
                    }
                } header: {
                    Text("Plugs the bike takes")
                }
                Section {
                    ForEach(allConnectors, id: \.self) { c in
                        Toggle(label(c), isOn: binding(for: c, in: \.adapters))
                    }
                } header: {
                    Text("Adapters you carry")
                } footer: {
                    Text("Adapters belong to you, not the bike. Serpentine will look for these too.")
                }
                if let problem = draft.problem {
                    Section { Label(problem, systemImage: "exclamationmark.triangle.fill").foregroundStyle(.orange) }
                }
            }
            .navigationTitle(isNew ? "New bike" : draft.name)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") {
                        var saved = draft
                        saved.estimated = false // the rider has looked at the numbers now
                        if isNew { garage.add(saved) } else { garage.update(saved) }
                        dismiss()
                    }
                    .disabled(draft.problem != nil)
                }
            }
        }
    }

    private func binding(for connector: String, in keyPath: WritableKeyPath<Bike, [String]>) -> Binding<Bool> {
        Binding(
            get: { draft[keyPath: keyPath].contains(connector) },
            set: { on in
                var list = draft[keyPath: keyPath]
                list.removeAll { $0 == connector }
                if on { list.append(connector) }
                draft[keyPath: keyPath] = list.sorted()
            })
    }

    private func stepperRow(_ title: String, value: Binding<Double>, step: Double, unit: String, format: String) -> some View {
        Stepper(value: value, in: 0.1...60, step: step) {
            LabeledContent(title, value: String(format: "\(format) \(unit)", value.wrappedValue))
        }
    }

    /// Riders think in miles of range, the model thinks in Wh/km. Convert at the edge.
    private func rangeRow(_ title: String, wh: Binding<Double>) -> some View {
        let miles = Binding<Double>(
            get: { draft.usableKWh / (wh.wrappedValue / 1000) / 1.609344 },
            set: { m in if m > 0 { wh.wrappedValue = draft.usableKWh / (m * 1.609344) * 1000 } })
        return Stepper(value: miles, in: 5...400, step: 1) {
            LabeledContent(title, value: "\(Int(miles.wrappedValue.rounded())) mi")
        }
    }
}
