import SwiftUI

struct RepoGroupHeader: View {
    let repo: Repo

    var body: some View {
        HStack(spacing: 6) {
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
    }
}
