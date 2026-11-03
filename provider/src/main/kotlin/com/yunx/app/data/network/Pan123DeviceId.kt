package com.yunx.app.data.network
// The Go process persists and supplies this NAS installation identifier.
object Pan123DeviceId {
    fun value(): String = System.getenv("YUNX_PROVIDER_DEVICE_ID")
        ?.takeIf { Regex("[a-f0-9]{32}").matches(it) }
        ?: throw IllegalStateException("Missing persistent provider device ID")
}
