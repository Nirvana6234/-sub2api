namespace LanAi.RelayClient.Services;

/// <summary>
/// The models one billing group serves, in the order Codex should offer them.
/// </summary>
/// <remarks>
/// <para>
/// One value feeds three places that must agree: the catalog the relay hands Codex's model
/// picker, the model the relay substitutes when Codex asks for one the group does not serve,
/// and the <c>model =</c> line written to <c>config.toml</c>. Deriving each separately is how
/// the picker ends up offering a model the relay then rewrites.
/// </para>
/// <para>
/// Built from a switched-on whitelist. A group without one has no definite list of its
/// own — every model the pool serves is fair game — so there is nothing to show and the
/// caller passes null, which leaves Codex on its bundled list. The exception is a Claude group
/// without a whitelist: its model is the one chosen on the Codex page, so the list is that
/// model alone (<see cref="Pinned"/>) — the relay then sends it whatever Codex asks for, and a
/// change on the page takes effect on the next turn.
/// </para>
/// </remarks>
internal sealed class CodexGroupModels
{
    private CodexGroupModels(IReadOnlyList<string> models, bool isPinned = false)
    {
        Models = models;
        IsPinned = isPinned;
    }

    /// <summary>The whitelisted models, the default first and the rest in ordinal order.</summary>
    public IReadOnlyList<string> Models { get; }

    /// <summary>
    /// Not a whitelist but one model the user chose on the Codex page (<see cref="Pinned"/>);
    /// for what the log says about it.
    /// </summary>
    public bool IsPinned { get; }

    /// <summary>Just <paramref name="model"/>: every request is sent to it.</summary>
    public static CodexGroupModels Pinned(string model)
    {
        ArgumentException.ThrowIfNullOrWhiteSpace(model);
        return new CodexGroupModels([model.Trim()], isPinned: true);
    }

    /// <summary>What Codex is moved to when it asks for something the group does not serve.</summary>
    public string DefaultModel => Models[0];

    /// <summary>
    /// A string that differs exactly when the picker Codex would show differs. What the
    /// dashboard compares to decide whether a group switch needs a restart to be seen.
    /// </summary>
    public string Signature => string.Join('\n', Models);

    /// <summary>Whether the group serves <paramref name="model"/>. Model ids are not case-sensitive to the server.</summary>
    public bool Contains(string model) =>
        Models.Any(m => string.Equals(m, model, StringComparison.OrdinalIgnoreCase));

    /// <summary>
    /// The group's models, or null when it has none of its own.
    /// </summary>
    /// <param name="allowed">The group's whitelist; entries that are blank or repeated are ignored.</param>
    /// <param name="preferred">
    /// The model the user chose for this group, if any. It becomes the default only when the
    /// group actually serves it — a choice carried over from another group must not put a
    /// model the new one refuses at the head of the list.
    /// </param>
    public static CodexGroupModels? From(IEnumerable<string>? allowed, string? preferred = null)
    {
        if (allowed is null)
        {
            return null;
        }

        List<string> distinct = [.. allowed
            .Where(m => !string.IsNullOrWhiteSpace(m))
            .Select(m => m.Trim())
            .Where(IsConcrete)
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .Order(NewestFirst.Instance)];
        if (distinct.Count == 0)
        {
            return null;
        }

        if (!string.IsNullOrWhiteSpace(preferred))
        {
            int at = distinct.FindIndex(m => string.Equals(m, preferred.Trim(), StringComparison.OrdinalIgnoreCase));
            if (at > 0)
            {
                string chosen = distinct[at];
                distinct.RemoveAt(at);
                distinct.Insert(0, chosen);
            }
        }

        return new CodexGroupModels(distinct);
    }

    /// <summary>The signature of <paramref name="models"/>, empty for a group that has none.</summary>
    public static string SignatureOf(CodexGroupModels? models) => models?.Signature ?? string.Empty;

    /// <summary>
    /// Whether <paramref name="id"/> names one model. A whitelist may hold a pattern
    /// (<c>gpt-5*</c>) saying what the group admits; that is worth showing in a tip but is not
    /// something to put in a picker or send as a model name.
    /// </summary>
    public static bool IsConcrete(string id) => !id.Contains('*') && !id.Contains('?');

    /// <summary>
    /// Newest version first, so the default — the head of the list — is the latest model rather
    /// than whichever sorts first alphabetically (which put <c>gpt-5.2</c> ahead of
    /// <c>gpt-6</c>). Numbers compare as numbers; where one id is the other plus a suffix
    /// (<c>gpt-5.6</c>, <c>gpt-5.6-sol</c>) the plain one comes first.
    /// </summary>
    private sealed class NewestFirst : IComparer<string>
    {
        public static readonly NewestFirst Instance = new();

        public int Compare(string? x, string? y)
        {
            string[] a = Tokens(x ?? string.Empty);
            string[] b = Tokens(y ?? string.Empty);
            for (int i = 0; i < Math.Min(a.Length, b.Length); i++)
            {
                int c = CompareToken(a[i], b[i]);
                if (c != 0)
                {
                    return -c; // larger version first
                }
            }

            int byLength = a.Length.CompareTo(b.Length);
            return byLength != 0 ? byLength : string.Compare(x, y, StringComparison.Ordinal);
        }

        private static int CompareToken(string a, string b)
        {
            bool aDigits = char.IsAsciiDigit(a[0]);
            bool bDigits = char.IsAsciiDigit(b[0]);
            if (aDigits && bDigits)
            {
                string ta = a.TrimStart('0');
                string tb = b.TrimStart('0');
                int byLength = ta.Length.CompareTo(tb.Length);
                return byLength != 0 ? byLength : string.CompareOrdinal(ta, tb);
            }

            if (aDigits != bDigits)
            {
                return aDigits ? 1 : -1;
            }

            return string.Compare(a, b, StringComparison.OrdinalIgnoreCase);
        }

        private static string[] Tokens(string id)
        {
            var tokens = new List<string>();
            int start = 0;
            for (int i = 1; i <= id.Length; i++)
            {
                if (i == id.Length || char.IsAsciiDigit(id[i]) != char.IsAsciiDigit(id[i - 1]))
                {
                    tokens.Add(id[start..i]);
                    start = i;
                }
            }

            return [.. tokens.Where(t => t.Length > 0)];
        }
    }
}
