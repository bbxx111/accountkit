import 'package:accountkit_flutter/accountkit_flutter.dart';
import 'package:accountkit_flutter/testing.dart';
import 'package:flutter/material.dart';

// Standalone UI demonstration without platform channels or real credentials.
// In an Android/iOS application replace FakeAccountApi with AccountClient(
//   baseUrl: 'https://accounts.example.com', apiPrefix: '/v1');
void main() => runApp(const MaterialApp(home: AccountExample()));

class AccountExample extends StatefulWidget {
  const AccountExample({super.key});
  @override
  State<AccountExample> createState() => _AccountExampleState();
}

class _AccountExampleState extends State<AccountExample> {
  late final AccountController controller;
  final email = TextEditingController(), code = TextEditingController();
  @override
  void initState() {
    super.initState();
    controller = AccountController(
      api: FakeAccountApi(),
      deviceProvider: FixedDeviceProvider(const DeviceSnapshot(id: 'example')),
      ownsApi: true,
    );
    controller.addListener(_changed);
    _run(controller.initialize);
  }

  void _changed() {
    if (mounted) setState(() {});
  }

  Future<void> _run(Future<void> Function() work) async {
    try {
      await work();
    } on AccountError {
      /* Render safe typed errors through controller.lastError. */
    }
  }

  @override
  void dispose() {
    controller.removeListener(_changed);
    controller.dispose();
    email.dispose();
    code.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) => Scaffold(
    appBar: AppBar(title: const Text('accountkit example')),
    body: Padding(
      padding: const EdgeInsets.all(24),
      child: Column(
        children: [
          Text(
            controller.initialized
                ? controller.session.state.runtimeType.toString()
                : 'Initializing',
          ),
          TextField(
            controller: email,
            onChanged: (_) => controller.changeInput(),
            decoration: const InputDecoration(labelText: 'Email'),
          ),
          ElevatedButton(
            onPressed: () => _run(() async {
              await controller.sendCode(
                CodePurpose.signIn,
                'EMAIL',
                email.text,
              );
            }),
            child: const Text('Send code'),
          ),
          TextField(
            controller: code,
            decoration: const InputDecoration(labelText: 'Code'),
            obscureText: true,
          ),
          ElevatedButton(
            onPressed: () => _run(() async {
              await controller.submitCode(code.text);
            }),
            child: const Text('Verify and continue'),
          ),
          if (controller.lastError != null)
            Text(controller.lastError!.runtimeType.toString()),
        ],
      ),
    ),
  );
}
