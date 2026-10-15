using System.ComponentModel;
using System.IO;
using System.Text;
using System.Text.Json;

namespace AgentDock.ControlPanel;

public readonly record struct PayloadPageSlice(string Text, long Offset, long NextOffset, bool HasMore, int Scalars);

public sealed class ExecutionPayloadView(string kind) : INotifyPropertyChanged
{
    public const int PageBytes = 32768;
    private const int OverviewScalars = 240;
    private readonly List<long> _previous = [];
    private bool _loaded;
    private bool _fullyLoaded;
    private bool _knownBytes;
    private int _displayedChars;
    private PayloadPageBudget? _loadedBudget;
    private string _readError = "", _storageReason = "";
    public event PropertyChangedEventHandler? PropertyChanged;
    public string State { get; private set; } = "unknown";
    public string Reference { get; private set; } = "";
    public string Reason => _readError.Length > 0 ? _readError : _storageReason;
    public string Text { get; private set; } = "";
    public string Overview { get; private set; } = "";
    public long TotalBytes { get; private set; }
    public long Lines { get; private set; }
    public long Offset { get; private set; }
    public long NextOffset { get; private set; }
    public bool HasMore { get; private set; }
    public bool HasPrevious => _previous.Count > 0;
    public bool HasStoredContent => Reference.Length > 0;
    public bool NeedsLoad => HasStoredContent && !_loaded;
    public bool IsFullyLoaded => _fullyLoaded;
    public bool Busy { get; private set; }
    public bool CanReadNext => HasMore && !Busy;
    public bool CanReadPrevious => HasPrevious && !Busy;
    public bool CanLoadAll => HasStoredContent && !_fullyLoaded && !Busy;
    public bool HasReadError => _readError.Length > 0 && HasStoredContent;
    public string SizeLabel => !_knownBytes ? "" : FormatBytes(TotalBytes);
    private string ContentName => kind == "调用" ? "调用参数" : "输出正文";
    public string Position
    {
        get
        {
            if (State == "unknown") return $"该记录未保存{ContentName}";
            if (!HasStoredContent) return Reason.Length > 0 ? StateLabel + "：" + Reason : StateLabel;
            if (!_knownBytes) return $"当前显示 {_displayedChars:N0} 字符，总长度暂未知";
            if (TotalBytes == 0) return State == "streaming" ? "当前已保存 0 字节，输出继续写入" : $"{ContentName}为空，0 字节";
            if (!_loaded)
            {
                var prefix = State == "partial" ? $"当前为预览，已保存部分内容共 {TotalBytes:N0} 字节" : $"当前为预览，完整载荷共 {TotalBytes:N0} 字节";
                return kind == "调用" ? prefix + "；选择“显示全部”查看完整参数" : prefix;
            }
            if (_fullyLoaded) return State == "partial"
                ? $"已显示已保存的部分{ContentName}，共 {TotalBytes:N0} 字节" + (_storageReason.Length > 0 ? "；" + _storageReason : "")
                : $"已显示全部{ContentName}，共 {TotalBytes:N0} 字节";
            var range = NextOffset > Offset ? $"当前页为第 {Offset + 1:N0}–{NextOffset:N0} 字节" : "当前页无内容";
            var suffix = kind == "调用" ? "；可选择“显示全部”合并完整参数" : "";
            return State switch
            {
                "streaming" => $"{range}，当前已保存 {TotalBytes:N0} 字节，输出继续写入{suffix}",
                "partial" => $"{range}，已保存部分内容共 {TotalBytes:N0} 字节" + (_storageReason.Length > 0 ? "；" + _storageReason : "") + suffix,
                _ => $"{range}，完整载荷共 {TotalBytes:N0} 字节{suffix}"
            };
        }
    }
    public string StateLabel => State switch
    {
        "pending" => kind == "调用" ? "正在保存调用参数" : "等待输出",
        "streaming" => "流式输出", "complete" => "已保存", "partial" => "部分输出",
        "not_stored" => "未保存", "internal" => "内部调用", _ => "旧记录"
    };

    public void Describe(JsonElement descriptor, string parentCallId = "")
    {
        var present = descriptor.ValueKind == JsonValueKind.Object;
        var reference = descriptor.Text("ref");
        var preview = descriptor.Text("preview");
        var overview = descriptor.Text("overview");
        var reset = Reference != reference;
        State = present ? descriptor.Text("state", "unknown") : "unknown";
        _storageReason = descriptor.Text("reason");
        _knownBytes = descriptor.OptionalNumber("bytes") is >= 0;
        TotalBytes = descriptor.Number("bytes"); Lines = descriptor.Number("lines");
        Reference = reference;
        if (reset)
        {
            _readError = ""; _loaded = false; _fullyLoaded = false; _loadedBudget = null;
            _previous.Clear(); Offset = NextOffset = 0; HasMore = false; Text = Overview = ""; _displayedChars = 0;
        }
        if (overview.Length > 0) Overview = CompactOverview(overview);
        else if (Overview.Length == 0 && preview.Length > 0) Overview = CompactOverview(preview);
        if (!_loaded)
        {
            if (preview.Length > 0 || reset || Text.Length == 0)
            {
                Text = preview;
                _displayedChars = ToolOutputSettings.ScalarCount(Text);
            }
            if (Text.Length == 0 && !HasStoredContent)
                Text = Reason.Length > 0 ? Reason : !present || State == "unknown" ? $"旧记录未保存{kind}。" : StateLabel + "。";
        }
        if (!present && parentCallId.Length > 0)
        {
            State = "internal";
            Text = "此内部调用关联根调用 " + parentCallId + "。已保存的业务请求与输出请在根调用中查看。";
            Overview = "";
        }
        Notify();
    }

    public long RequestedOffset(bool previous)
    {
        if (previous) return _previous.Count > 0 ? _previous[^1] : 0;
        return _loaded && HasMore ? NextOffset : 0;
    }
    public void SetBusy(bool busy) { Busy = busy; Notify(); }
    public bool NeedsBudgetReload(PayloadPageBudget budget) => _loaded && !_fullyLoaded && _loadedBudget != budget;
    public bool TryReadPage(JsonElement page, string expectedReference, PayloadPageBudget budget, out PayloadPageSlice slice)
    {
        slice = default;
        if (Reference != expectedReference || page.Field("payload").Text("ref") != expectedReference) return false;
        var offset = page.Number("offset");
        var next = page.Number("next_offset");
        var text = page.Text("text");
        var scalars = ToolOutputSettings.ScalarCount(text);
        if (offset < 0 || next < offset || _knownBytes && next > TotalBytes || next - offset > budget.MaxBytes || Encoding.UTF8.GetByteCount(text) != next - offset)
            throw new InvalidDataException("工具输出分页边界无效。");
        if (budget.LimitChars > 0 && (scalars > budget.LimitChars || page.Text("unit") != "unicode_scalar" || page.Number("limit_chars") != budget.LimitChars || page.Number("returned_chars") != scalars))
            throw new InvalidDataException("工具输出字符预算或计数不一致。");
        var hasMore = page.Flag("has_more");
        if (hasMore && next <= offset) throw new InvalidDataException("工具输出分页没有推进。");
        slice = new(text, offset, next, hasMore, scalars);
        return true;
    }
    public bool ApplyPage(JsonElement page, string expectedReference, bool previous, PayloadPageBudget? requestedBudget = null)
    {
        var budget = requestedBudget ?? PayloadPageBudget.BytesOnly;
        if (!TryReadPage(page, expectedReference, budget, out var slice)) return false;
        if (NeedsBudgetReload(budget))
        {
            if (slice.Offset != 0) throw new InvalidDataException("修改字符上限后必须从已知起点重新分页。");
            _previous.Clear(); _loaded = false;
        }
        if (_loaded && slice.Offset != Offset)
        {
            if (previous && _previous.Count > 0) _previous.RemoveAt(_previous.Count - 1);
            else if (!previous) _previous.Add(Offset);
        }
        Offset = slice.Offset; NextOffset = slice.NextOffset; HasMore = slice.HasMore;
        Text = slice.Text; _displayedChars = slice.Scalars; _loaded = true; _loadedBudget = budget; _readError = "";
        _fullyLoaded = slice.Offset == 0 && !slice.HasMore && (!_knownBytes || slice.NextOffset == TotalBytes);
        Notify(); return true;
    }
    public bool ApplyComplete(string text, string expectedReference)
    {
        if (Reference != expectedReference) return false;
        var bytes = Encoding.UTF8.GetByteCount(text);
        if (_knownBytes && bytes != TotalBytes) throw new InvalidDataException("完整调用参数的字节数与记录不一致。");
        Text = text; _displayedChars = ToolOutputSettings.ScalarCount(text);
        Offset = 0; NextOffset = bytes; HasMore = false; _loaded = true; _fullyLoaded = true; _loadedBudget = null; _readError = "";
        _previous.Clear(); Notify(); return true;
    }
    public void ReadFailed(string reason)
    {
        _readError = reason;
        if (!_loaded && Text.Length == 0) Text = reason;
        Notify();
    }
    private static string CompactOverview(string text)
    {
        if (string.IsNullOrWhiteSpace(text)) return "";
        var compact = text;
        try
        {
            using var parsed = JsonDocument.Parse(text);
            compact = JsonSerializer.Serialize(parsed.RootElement);
        }
        catch (JsonException)
        {
            compact = text.Replace('\r', ' ').Replace('\n', ' ').Replace('\t', ' ').Trim();
        }
        var index = 0; var scalars = 0;
        while (index < compact.Length && scalars < OverviewScalars)
        {
            index += char.IsHighSurrogate(compact[index]) && index + 1 < compact.Length && char.IsLowSurrogate(compact[index + 1]) ? 2 : 1;
            scalars++;
        }
        return index < compact.Length ? compact[..index] + "…" : compact;
    }
    private static string FormatBytes(long bytes) => bytes switch
    {
        < 1024 => $"{bytes:N0} B",
        < 1024 * 1024 => $"{bytes / 1024d:0.#} KiB",
        _ => $"{bytes / (1024d * 1024d):0.#} MiB"
    };
    private void Notify() => PropertyChanged?.Invoke(this, new(null));
}
