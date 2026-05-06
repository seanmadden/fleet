import SwiftUI

struct SessionRow: View {
    let session: Session

    var body: some View {
        HStack(spacing: 8) {
            Text(session.status.icon)
                .foregroundStyle(session.status.tint)
                .font(.system(.body, design: .monospaced))
                .frame(width: 12, alignment: .center)

            Text(session.title)
                .lineLimit(1)
                .truncationMode(.tail)

            if let slot = session.slot {
                Text("[\(slot)]")
                    .font(.caption2.monospaced())
                    .foregroundStyle(.secondary)
            }

            Spacer(minLength: 0)

            if !session.isAlive {
                Text("✕")
                    .font(.caption2)
                    .foregroundStyle(.red.opacity(0.8))
            }
        }
        .contentShape(Rectangle())
    }
}
