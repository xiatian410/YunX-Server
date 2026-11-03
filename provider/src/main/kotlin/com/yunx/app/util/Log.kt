package com.yunx.app.util
// Protocol errors are returned through the private IPC channel. Never log credentials or URLs.
object Log { fun d(vararg ignored: Any?) {}; fun w(vararg ignored: Any?) {} ; fun i(vararg ignored: Any?) {} }
object LogRedactor { fun url(ignored: String) = "[redacted]" }
object DiagnosticLog { const val NETWORK = "network"; fun warn(vararg ignored: Any?) {} }
