import SwiftUI

struct SidebarView: View {
    @Bindable var model: AppModel

    var body: some View {
        VStack(spacing: 0) {
            filterBar
            Divider()
            sessionList
        }
    }

    private var filterBar: some View {
        HStack {
            Image(systemName: "magnifyingglass")
                .foregroundStyle(.secondary)
            TextField("Filter sessions", text: $model.filterText)
                .textFieldStyle(.plain)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
    }

    private var sessionList: some View {
        List(selection: $model.selectedSessionID) {
            ForEach(filteredRepos, id: \.id) { repo in
                Section(header: RepoGroupHeader(repo: repo)) {
                    ForEach(repo.sessions, id: \.id) { session in
                        SessionRow(session: session)
                            .tag(session.id as String?)
                    }
                }
            }
        }
        .listStyle(.sidebar)
    }

    private var filteredRepos: [Repo] {
        let needle = model.filterText.lowercased()
        let repos = model.displayedRepos
        guard !needle.isEmpty else { return repos.filter { !$0.sessions.isEmpty || $0.pinned } }

        return repos.compactMap { repo in
            let matchedSessions = repo.sessions.filter { sess in
                sess.title.lowercased().contains(needle)
                    || repo.displayName.lowercased().contains(needle)
            }
            guard !matchedSessions.isEmpty else { return nil }
            var copy = repo
            copy.sessions = matchedSessions
            return copy
        }
    }
}
