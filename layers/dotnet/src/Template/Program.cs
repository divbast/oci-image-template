// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// The .NET layer's demo: the template's page on /, and ok on /healthz, each
// to GET or HEAD alone, and an empty 404 to everything else. 8080 is an
// allowlist, as the Go core keeps it, and /healthz sits on it beside the
// page, since an upstream .NET app has one port. Kestrel listens where
// ASPNETCORE_HTTP_PORTS says, which the image sets to 8080.

var app = WebApplication.CreateSlimBuilder(args).Build();
// The one handler, with nothing before or after it, then the host
app.Run(Answer);
app.Run();

static Task Answer(HttpContext context)
{
    var request = context.Request;
    var response = context.Response;
    var body = request.Path.Value switch
    {
        "/" => "Hello from oci-image-template",
        "/healthz" => "ok",
        _ => null,
    };
    var head = HttpMethods.IsHead(request.Method);
    if (body is null || !(head || HttpMethods.IsGet(request.Method)))
    {
        response.StatusCode = StatusCodes.Status404NotFound;
        return Task.CompletedTask;
    }
    response.ContentType = "text/plain; charset=utf-8";
    return head ? Task.CompletedTask : response.WriteAsync(body);
}
