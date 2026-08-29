using System.Xml.Linq;
using LanAi.RelayClient.Platform.MacOS;
using Xunit;

namespace LanAi.RelayClient.Tests;

/// <summary>
/// Covers the half of macOS autostart that can be checked without a Mac.
/// </summary>
/// <remarks>
/// The file write and the <c>launchctl</c> call cannot be exercised here; the plist
/// content can, and it is where the silent failures live. <c>launchd</c> does not
/// report a malformed agent to anyone — it ignores the file, and the user simply
/// finds the client did not start.
/// </remarks>
public class LaunchAgentPlistTests
{
    private const string Executable = "/Applications/共飞-ChatGPT助手.app/Contents/MacOS/LanAi.RelayClient";

    [Fact]
    public void ProducesParseableXml()
    {
        XDocument document = XDocument.Parse(LaunchAgentPlist.Build(Executable));
        Assert.Equal("plist", document.Root!.Name.LocalName);
    }

    [Fact]
    public void CarriesLabelExecutableAndRunAtLoad()
    {
        XDocument document = XDocument.Parse(LaunchAgentPlist.Build(Executable));
        XElement dict = document.Root!.Element("dict")!;
        List<XElement> children = [.. dict.Elements()];

        int labelIndex = children.FindIndex(e => e.Name == "key" && e.Value == "Label");
        Assert.Equal(LaunchAgentPlist.Label, children[labelIndex + 1].Value);

        int argsIndex = children.FindIndex(e => e.Name == "key" && e.Value == "ProgramArguments");
        Assert.Equal(Executable, children[argsIndex + 1].Element("string")!.Value);

        int runIndex = children.FindIndex(e => e.Name == "key" && e.Value == "RunAtLoad");
        Assert.Equal("true", children[runIndex + 1].Name.LocalName);
    }

    /// <remarks>
    /// A volume or home folder named with an ampersand is ordinary, not exotic —
    /// "Bob &amp; Alice's iMac" is a name a person would choose. Unescaped, it makes the
    /// whole plist unparseable and autostart silently stops working.
    /// </remarks>
    [Theory]
    [InlineData("/Volumes/Bob & Alice/App.app/Contents/MacOS/Client")]
    [InlineData("/Users/a<b>c/App.app/Contents/MacOS/Client")]
    [InlineData("/Users/tom&jerry/x<y>z/Client")]
    public void EscapesPathsThatWouldOtherwiseBreakTheXml(string path)
    {
        string plist = LaunchAgentPlist.Build(path);

        XDocument document = XDocument.Parse(plist);
        XElement array = document.Descendants("array").Single();

        // Round-trips to the original path: escaped on the way in, decoded on the way
        // out. Asserting on the raw text would only prove that something was written.
        Assert.Equal(path, array.Element("string")!.Value);
    }

    [Fact]
    public void FileNameMatchesTheLabelSoLaunchctlCanFindIt()
    {
        Assert.Equal(LaunchAgentPlist.Label + ".plist", LaunchAgentPlist.FileName);
    }

    /// <remarks>
    /// Unix line endings regardless of the machine that built the file. A plist
    /// generated on the Windows build host must still be a plist on macOS.
    /// </remarks>
    [Fact]
    public void UsesUnixLineEndingsEvenWhenBuiltOnWindows()
    {
        Assert.DoesNotContain('\r', LaunchAgentPlist.Build(Executable));
    }

    [Fact]
    public void RefusesAnEmptyExecutablePath()
    {
        Assert.Throws<ArgumentException>(() => LaunchAgentPlist.Build("  "));
    }
}
