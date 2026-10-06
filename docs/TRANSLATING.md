# Translating OMABUDGET

A translation is a data file. There is no compile step, no toolchain beyond the
one the plugin already needs, and nothing to register: `i18n/<tag>.json` is read
at runtime and that is the whole mechanism.

## Adding a language

1. Copy `i18n/en.json` to `i18n/<tag>.json`, where the tag is a BCP 47 style
   name such as `fr` or `pt-PT`. It may contain letters, digits and hyphens and
   nothing else, because the tag becomes a filename.
2. Set `locale` to the Qt locale the language should format figures and dates
   with, such as `fr_FR`, and `name` to the language's own name for itself. The
   switcher shows `name`, and `locale` decides the decimal point, the group
   separator and the month names.
3. Translate every value. Leave the keys alone.
4. Add the tag to `available` in `i18n/I18n.qml`, in alphabetical order by tag.
5. Run the guards:

   ```
   go test ./guard/
   ```

They fail on a key the interface asks for and the file does not carry, a key
English does not have, a placeholder such as `%1` that moved or went missing, a
keyboard shortcut a legend dropped, and a tag the switcher offers with no file
behind it.

While you work, the app reloads the file as you save it, so a second window
open on Settings is the fastest way to proofread.

## Category names are different

The keys under `seed.category.` are not labels on the screen. They are the
names of the categories the ledger plants on first run, which are rows in the
database, and the interface renders whatever the row says. Translating them
means renaming those rows, which Settings does on request under INTERFACE,
never on its own.

Three consequences worth knowing while translating them:

- They are the one family where a missing key fails the build. Everywhere else
  a missing key falls back to English and costs one label; here it would leave
  a category standing in another language.
- Two categories under the same parent must not end up with the same name. The
  ledger treats a duplicate as ambiguous, and the whole rename is refused
  rather than half applied, so a guard checks this before it can ship.
- You will not see them change in the running app by saving the file. The
  daemon reads these from the build rather than from disk, so they need a
  `make` and then the button in Settings.

## What a translation may assume

- Strings are formatted with `%1`, `%2` and so on, and a translation may move
  them around. The order in the English string is not binding.
- Plurals are not handled. Where a count reads badly in both forms, prefer a
  phrasing that works for one and many.
- A run of three or more spaces in a legend separates a keyboard key from what
  it does, as in `j k move   Enter edit`. Keep the single letters: they are the
  keys that are actually bound.
- Dates and separators come from `locale`, so do not write them into strings.
