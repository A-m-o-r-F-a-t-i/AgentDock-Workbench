package dev.agentdock.workbench.shizuku;

// Control calls are short. Process lifetime and output belong to the original ID.
interface IPhoneShell {
    String control(String nodeId, String request) = 0;
    void destroy() = 16777114;
}
