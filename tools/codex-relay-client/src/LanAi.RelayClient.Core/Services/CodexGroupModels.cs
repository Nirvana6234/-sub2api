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
/// Built only from a switched-on whitelist. A group without one has no definite list of its
/// own — every model the pool serves is fair game — so there is nothing to show and the
/// caller passes null, which leaves Codex on its bundled list.
/// </para>
/// </remarks>
internal sealed class CodexGroupModels
{
    private CodexGroupModels(IReadOnlyList<string> models) => Models = models;

    /// <summary>The whitelisted models, the default first and the rest in ordinal order.</summary>
    public IReadOnlyList<string> Models { get; }

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
            .Distinct(StringComparer.OrdinalIgnoreCase)
            .OrderBy(m => m, StringComparer.Ordinal)];
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
}
