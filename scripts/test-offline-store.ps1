$ErrorActionPreference = "Stop"

$clientRoot = "E:\code\mesh-proxy"
$serverRoot = "E:\code\meshchat-store\store-node"

Write-Host "Running client offline-store tests..."
Push-Location $clientRoot
try {
    go test ./internal/chat -parallel 1 -run "TestPeekOfflineStoreSeq|TestIsPermanentOfflineFetchItemError|TestProcessOneOfflineFetchItem_RepairsMissingSessionState|TestProcessOneOfflineFetchItem_MalformedJSONIsPermanent|TestValidateOfflineEnvelopeTiming"
} finally {
    Pop-Location
}

Write-Host "Running server offline-store tests..."
Push-Location $serverRoot
try {
    go test ./internal/p2p -run "TestStoreFetchAckProtocols|TestUnauthorizedFetch|TestUnauthorizedAck|TestStoreInvalidSignature|TestInvalidSignatureDoesNotConsumeSenderRateLimit"
    go test ./internal/storage/pebble -run "TestStoreFetchAckAndDuplicates|TestFetchFiltersExpiredBacklog|TestStoreMessageCleansStaleDuplicate|TestAckMessagesRejectsUnfetchedSeq|TestFetchDoesNotAdvanceDeliveredFrontier"
} finally {
    Pop-Location
}

Write-Host "Offline-store tests completed."
