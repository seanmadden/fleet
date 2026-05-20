---
type: fixed
---
`Shift+↑`/`Shift+↓` on individual sessions in a group of three or more no longer shoves the moved pair to the bottom of the group. Previously the first reorder in any group seeded sort keys only for the two swapped sessions, leaving the rest at the legacy `SortKey=0` sentinel — those zero-key siblings then sorted ahead of the seeded pair in the global order, so swapping the top two in `[A, B, C, D]` produced `[C, D, B, A]` instead of `[B, A, C, D]`. Reorder now renumbers every session in the group, matching how repo-group reorder already worked.
