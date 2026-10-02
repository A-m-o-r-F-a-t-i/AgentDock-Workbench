using System.Collections.Concurrent;
using System.IO;
using System.Net;
using System.Net.Sockets;
using System.Text;

internal sealed class HttpFixture : IAsyncDisposable
{
    internal sealed record Request(string Method, string Target, IReadOnlyDictionary<string, string> Headers, string Body);
    internal sealed record Response(int Status, string Body = "{}", string ExtraHeaders = "", int DelayMilliseconds = 0);
    private readonly TcpListener _listener;
    private readonly CancellationTokenSource _stop = new();
    private readonly Task _accept;
    private readonly List<Task> _connections = [];
    private int _count;
    internal Func<Request, Response> Respond { get; set; }
    internal ConcurrentQueue<Request> Requests { get; } = new();
    internal string Address => "127.0.0.1:" + Port;
    internal string Origin => "http://" + Address;
    internal int Port => ((IPEndPoint)_listener.LocalEndpoint).Port;
    internal int Count => Volatile.Read(ref _count);

    internal HttpFixture(Func<Request, Response> respond, bool anyAddress = false)
    {
        Respond = respond;
        _listener = new(anyAddress ? IPAddress.Any : IPAddress.Loopback, 0);
        _listener.Start();
        _accept = AcceptAsync();
    }

    private async Task AcceptAsync()
    {
        try
        {
            while (!_stop.IsCancellationRequested)
            {
                var client = await _listener.AcceptTcpClientAsync(_stop.Token);
                if (_connections.Count >= 100) { client.Dispose(); throw new IOException("Fixture connection bound exceeded."); }
                _connections.Add(HandleAsync(client));
            }
        }
        catch (OperationCanceledException) when (_stop.IsCancellationRequested) { }
        catch (SocketException) when (_stop.IsCancellationRequested) { }
    }

    private async Task HandleAsync(TcpClient client)
    {
        using (client)
        using (var deadline = CancellationTokenSource.CreateLinkedTokenSource(_stop.Token))
        {
            deadline.CancelAfter(TimeSpan.FromSeconds(10));
            try
            {
                var stream = client.GetStream();
                using var reader = new StreamReader(stream, Encoding.UTF8, false, 1024, true);
                var first = await reader.ReadLineAsync(deadline.Token) ?? throw new IOException("Missing request.");
                var parts = first.Split(' ', 3);
                if (parts.Length != 3) throw new IOException("Invalid request line.");
                var headers = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
                var headerSize = first.Length;
                while (await reader.ReadLineAsync(deadline.Token) is { Length: > 0 } line)
                {
                    headerSize += line.Length;
                    if (headerSize > 16384) throw new IOException("Request headers exceeded fixture bound.");
                    var separator = line.IndexOf(':');
                    if (separator < 0) throw new IOException("Invalid header.");
                    headers[line[..separator]] = line[(separator + 1)..].Trim();
                }
                var body = "";
                if (headers.TryGetValue("Content-Length", out var length))
                {
                    var count = int.Parse(length);
                    if (count is < 0 or > 8192) throw new IOException("Fixture body limit.");
                    var chars = new char[count];
                    var offset = 0;
                    while (offset < chars.Length)
                    {
                        var read = await reader.ReadAsync(chars.AsMemory(offset), deadline.Token);
                        if (read == 0) throw new EndOfStreamException("Incomplete fixture request body.");
                        offset += read;
                    }
                    body = new string(chars);
                }
                var request = new Request(parts[0], parts[1], headers, body);
                Requests.Enqueue(request);
                Interlocked.Increment(ref _count);
                var response = Respond(request);
                if (response.DelayMilliseconds > 0) await Task.Delay(response.DelayMilliseconds, deadline.Token);
                var bytes = Encoding.UTF8.GetBytes(response.Body);
                var head = Encoding.ASCII.GetBytes($"HTTP/1.1 {response.Status} Fixture\r\nContent-Type: application/json\r\nContent-Length: {bytes.Length}\r\nConnection: close\r\n{response.ExtraHeaders}\r\n");
                await stream.WriteAsync(head, deadline.Token);
                await stream.WriteAsync(bytes, deadline.Token);
            }
            catch (OperationCanceledException) when (deadline.IsCancellationRequested) { }
            catch (IOException) { /* Cancellation may close the peer before the bounded fixture writes. */ }
        }
    }

    public async ValueTask DisposeAsync()
    {
        _stop.Cancel();
        _listener.Stop();
        await _accept;
        await Task.WhenAll(_connections);
        _stop.Dispose();
    }
}
