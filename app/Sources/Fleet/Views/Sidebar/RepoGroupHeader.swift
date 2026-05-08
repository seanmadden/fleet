import SwiftUI

struct RepoGroupHeader: View {
    let repo: Repo
    let model: AppModel

    var body: some View {
        HStack(spacing: 6) {
            if repo.pinned {
                Image(systemName: "pin.fill")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }

            Text(repo.displayName)
                .fontWeight(.semibold)
                .lineLimit(1)
                .truncationMode(.middle)

            Text("(\(repo.branch))")
                .font(.caption.monospaced())
                .foregroundStyle(.secondary)
                .lineLimit(1)

            if repo.dirty {
                Text("*")
                    .foregroundStyle(.orange)
                    .font(.caption.monospaced())
            }

            if let pr = repo.prInfo {
                Text("#\(pr.number)\(pr.state.glyph)")
                    .font(.caption.monospaced())
                    .foregroundStyle(pr.state.tint)
            }

            Spacer(minLength: 0)
        }
        .contextMenu {
            Button(repo.pinned ? "Unpin Repo" : "Pin Repo") {
                let root = repo.id
                let pinned = repo.pinned
                Task { await model.dispatchPinRepo(root: root, pinned: pinned) }
            }
        }
    }
}
